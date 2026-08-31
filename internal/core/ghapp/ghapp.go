// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package ghapp is the GitHub App client: signs JWTs with the App
// private key, mints + caches per-installation access tokens, and
// generates JIT runner configs.
//
// We deliberately do NOT pull in google/go-github - these three calls
// are the entire surface we need from GitHub, and a focused client
// keeps the dependency graph small.
package ghapp

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/sync/singleflight"
)

// apiBase is a var (not const) solely so tests can point the client
// at an httptest server. Production code never mutates it.
var apiBase = "https://api.github.com"

type Client struct {
	appID      int64
	privateKey *rsa.PrivateKey
	http       *http.Client

	mu     sync.Mutex
	tokens map[int64]*cachedToken // installation_id -> access token

	// sf collapses concurrent refreshes for the same installation into
	// one upstream call. Without it, two goroutines hitting a cold
	// cache for the same id both POST to GitHub and one result is
	// thrown away.
	sf singleflight.Group
}

type cachedToken struct {
	Token     string
	ExpiresAt time.Time
}

// New parses the App private key (PKCS1 or PKCS8 PEM) and returns a
// ready-to-use client.
func New(appID int64, privateKeyPath string) (*Client, error) {
	raw, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("private key: invalid PEM")
	}

	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if k2, err2 := x509.ParsePKCS8PrivateKey(block.Bytes); err2 == nil {
		rk, ok := k2.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key: not RSA")
		}
		key = rk
	} else {
		return nil, fmt.Errorf("parse private key: pkcs1=%v pkcs8=%v", err, err2)
	}

	return &Client{
		appID:      appID,
		privateKey: key,
		http:       &http.Client{Timeout: 30 * time.Second},
		tokens:     map[int64]*cachedToken{},
	}, nil
}

// AppJWT mints a 9-minute JWT signed with the App private key.
// GitHub caps app-JWT lifetime at 10 minutes. We use 9 + 30s skew to stay
// safely inside the window.
func (c *Client) AppJWT() (string, error) {
	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
		Issuer:    strconv.FormatInt(c.appID, 10),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return tok.SignedString(c.privateKey)
}

// InstallationToken returns a cached or freshly-minted installation
// access token.
// GitHub installation tokens last ~1h. We refresh 5m before expiry
// so calls don't race the boundary.
//
// Concurrent callers for the same installation collapse into one
// upstream POST via singleflight. The cache is re-checked under the
// lock inside the flight to absorb a token a sibling goroutine
// already minted.
func (c *Client) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	if t, ok := c.cachedTokenIfFresh(installationID); ok {
		return t, nil
	}
	key := strconv.FormatInt(installationID, 10)
	v, err, _ := c.sf.Do(key, func() (any, error) {
		if t, ok := c.cachedTokenIfFresh(installationID); ok {
			return t, nil
		}
		// The flight is shared by every concurrent caller, but this
		// closure captures only the INITIATING caller's ctx - if that
		// request is aborted (browser poll disconnects), its
		// cancellation would fail all waiters, including a runner's
		// JIT-config mint. Detach from the initiator's cancellation
		// and bound the fetch on its own timeout instead. The HTTP
		// client's 30s cap backstops it either way.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return c.fetchInstallationToken(fetchCtx, installationID)
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (c *Client) cachedTokenIfFresh(installationID int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.tokens[installationID]
	if !ok || time.Until(t.ExpiresAt) <= 5*time.Minute {
		return "", false
	}
	return t.Token, true
}

func (c *Client) fetchInstallationToken(ctx context.Context, installationID int64) (string, error) {
	appJWT, err := c.AppJWT()
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", apiBase, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("build installation-token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch installation token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", newAPIError("installation token", resp.StatusCode, resp.Status, body)
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode installation token: %w", err)
	}

	c.mu.Lock()
	c.tokens[installationID] = &cachedToken{Token: out.Token, ExpiresAt: out.ExpiresAt}
	c.mu.Unlock()
	return out.Token, nil
}

// JITConfig mints a just-in-time runner registration config for ONE
// ephemeral runner registered against a single repo. The returned
// string is fed to the runner via `--jitconfig <value>`. The runner
// registers, claims one job, and exits.
//
// Returns (encoded_jit_config, runner_id, err). The runner_id is
// GitHub's integer identity for the freshly-created runner. The
// caller stamps it on the instance row so the reaper can DELETE the
// runner from GitHub when the host is lost (fast-fails the
// workflow_job instead of waiting on heartbeat timeout).
//
// runnerGroupID is required for repo-level registration. The "Default"
// group on personal accounts is id 1.
func (c *Client) JITConfig(ctx context.Context, installationID int64, repoOwner, repoName, runnerName string, labels []string, runnerGroupID int) (string, int64, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/actions/runners/generate-jitconfig", apiBase, neturl.PathEscape(repoOwner), neturl.PathEscape(repoName))
	return c.mintJITConfig(ctx, installationID, url, runnerName, labels, runnerGroupID)
}

// JITConfigOrg mints a JIT config for an org-level runner. The runner
// shows up under Settings -> Actions -> Runners -> <runner_group> and
// can claim any job from any repo in the org that targets the
// corresponding labels.
//
// runnerGroupID 0 collapses to GitHub's "Default" group (id 1) so
// operators don't have to look up the id when they only have one group.
//
// Requires the GitHub App's `organization_self_hosted_runners: write`
// permission and the App installed on the org (not just specific
// repos).
func (c *Client) JITConfigOrg(ctx context.Context, installationID int64, orgName, runnerName string, labels []string, runnerGroupID int) (string, int64, error) {
	if runnerGroupID == 0 {
		runnerGroupID = 1
	}
	url := fmt.Sprintf("%s/orgs/%s/actions/runners/generate-jitconfig", apiBase, neturl.PathEscape(orgName))
	return c.mintJITConfig(ctx, installationID, url, runnerName, labels, runnerGroupID)
}

// DeleteRunnerRepo removes a self-hosted runner from a repository.
// When the runner has an active job, GitHub aborts that workflow_job
// immediately - the reaper uses this on instance.lost / reap so the
// workflow_job fails fast rather than hanging on the ~10-min
// heartbeat timeout.
//
// 404 from GitHub is treated as success: the runner has already been
// deregistered (ephemeral runners auto-deregister when run.sh exits
// cleanly, or GitHub purged it after its own timeout).
func (c *Client) DeleteRunnerRepo(ctx context.Context, installationID int64, repoOwner, repoName string, runnerID int64) error {
	url := fmt.Sprintf("%s/repos/%s/%s/actions/runners/%d", apiBase, neturl.PathEscape(repoOwner), neturl.PathEscape(repoName), runnerID)
	return c.deleteRunner(ctx, installationID, url)
}

// DeleteRunnerOrg is the org-scoped variant of DeleteRunnerRepo. Used
// when the project's scope is "org".
func (c *Client) DeleteRunnerOrg(ctx context.Context, installationID int64, orgName string, runnerID int64) error {
	url := fmt.Sprintf("%s/orgs/%s/actions/runners/%d", apiBase, neturl.PathEscape(orgName), runnerID)
	return c.deleteRunner(ctx, installationID, url)
}

func (c *Client) deleteRunner(ctx context.Context, installationID int64, url string) error {
	instTok, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("build delete-runner request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+instTok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("delete runner: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		body, _ := io.ReadAll(resp.Body)
		return newAPIError("delete runner", resp.StatusCode, resp.Status, body)
	}
}

// WorkflowJob fetches the current state of a workflow job (steps, status,
// conclusion, timing) from GitHub. Used by the job-detail endpoint to
// refresh `jobs.payload` while the modal is open, since the
// `workflow_job` webhook only fires on lifecycle transitions and steps[]
// is incomplete until the final `completed` event.
//
// Returns the raw 200 response body so the caller can wrap it back into
// the webhook envelope shape without re-marshalling. On any non-2xx
// status the error wraps the status code so the caller can distinguish
// permanent (404 - job removed) from transient (5xx, rate limit) and
// log appropriately.
func (c *Client) WorkflowJob(ctx context.Context, installationID int64, repoOwner, repoName string, jobID int64) ([]byte, error) {
	instTok, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/repos/%s/%s/actions/jobs/%d", apiBase, neturl.PathEscape(repoOwner), neturl.PathEscape(repoName), jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build workflow-job request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+instTok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch workflow job: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read workflow job: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, newAPIError("workflow job", resp.StatusCode, resp.Status, body)
	}
	return body, nil
}

// mintJITConfig is the shared POST-and-decode for both repo and org
// JIT-config endpoints. They differ only in the URL path. The
// installation token, request shape, and response shape are identical.
//
// Returns (encoded_jit_config, runner_id, err). runner_id is the
// integer identity GitHub assigns to the new ephemeral runner -
// stable for the runner's lifetime, used by DeleteRunner to abort
// the workflow_job when the host is lost.
func (c *Client) mintJITConfig(ctx context.Context, installationID int64, url, runnerName string, labels []string, runnerGroupID int) (string, int64, error) {
	instTok, err := c.InstallationToken(ctx, installationID)
	if err != nil {
		return "", 0, err
	}
	body, err := json.Marshal(map[string]any{
		"name":            runnerName,
		"runner_group_id": runnerGroupID,
		"labels":          labels,
		"work_folder":     "_work",
	})
	if err != nil {
		return "", 0, fmt.Errorf("marshal jitconfig body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("build jitconfig request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+instTok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("jitconfig: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return "", 0, newAPIError("jitconfig", resp.StatusCode, resp.Status, b)
	}
	var out struct {
		EncodedJITConfig string `json:"encoded_jit_config"`
		Runner           struct {
			ID int64 `json:"id"`
		} `json:"runner"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, fmt.Errorf("decode jitconfig: %w", err)
	}
	return out.EncodedJITConfig, out.Runner.ID, nil
}
