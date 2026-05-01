// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package ec2lt is the launch-template materializer.
// Given a Pool model (and the parent project's name + cascading tags), it
// validates the referenced AWS resources exist, then either
// CreateLaunchTemplate (first save) or CreateLaunchTemplateVersion +
// ModifyLaunchTemplate-default (subsequent saves).
// Mutates p.LaunchTemplateID and p.LaunchTemplateVersion in place.
//
// Per-spawn details (instance type, subnet, user-data, job-specific
// tags) are intentionally NOT in the LT - the orchestrator passes
// them as RunInstances overrides.
// The LT carries the static shape: AMI, security groups, IAM profile, root volume, IMDSv2,
// market options, base tags.
//
// Partial failure: if CreateLaunchTemplate succeeds but the pool's
// Put then fails, the LT is orphaned.
// V1 surfaces the error verbatim; operators can clean up via the EC2 console.
package ec2lt

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"

	"github.com/yousysadmin/pacer/internal/models/pool"
)

// ManagedByTagKey + ManagedByTagValue are stamped on every LT,
// instance, and volume so the IAM role's TerminateInstances policy
// can be tag-scoped (see docs/iam-role.json).
const (
	ManagedByTagKey   = "gha:managed-by"
	ManagedByTagValue = "pacer"
)

// CreateOrUpdate is the single entry point.
// Validates the pool's AWS references, then materializes the launch template.
// On the Create path: stamps p.LaunchTemplateID + Version.
// On the Update path: bumps version + sets it as default.
//
// iamc is optional -- when nil (e.g. running without iam:GetInstanceProfile
// in the role) the instance-profile check is skipped and a malformed
// or missing profile only surfaces at orchestrator spawn time.
func CreateOrUpdate(ctx context.Context, c *ec2.Client, iamc *iam.Client, p *pool.Pool, projectName string, projectTags map[string]string) error {
	ami, err := validateAMI(ctx, c, p)
	if err != nil {
		return err
	}
	if err := validateRootVolume(p, ami); err != nil {
		return err
	}
	if err := validateNetworking(ctx, c, p); err != nil {
		return err
	}
	if err := validateIAMInstanceProfile(ctx, iamc, p); err != nil {
		return err
	}

	data := buildLTData(p, ami, projectName, projectTags)

	if p.LaunchTemplateID == "" {
		out, err := c.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(ltNameFor(p, projectName)),
			LaunchTemplateData: data,
			TagSpecifications:  ltSelfTagSpecs(p, projectName, projectTags),
		})
		if err != nil {
			return fmt.Errorf("create launch template: %w", err)
		}
		p.LaunchTemplateID = aws.ToString(out.LaunchTemplate.LaunchTemplateId)
		p.LaunchTemplateVersion = int(aws.ToInt64(out.LaunchTemplate.LatestVersionNumber))
		return nil
	}

	out, err := c.CreateLaunchTemplateVersion(ctx, &ec2.CreateLaunchTemplateVersionInput{
		LaunchTemplateId:   aws.String(p.LaunchTemplateID),
		LaunchTemplateData: data,
	})
	if err != nil {
		return fmt.Errorf("create launch template version: %w", err)
	}
	newVersion := aws.ToInt64(out.LaunchTemplateVersion.VersionNumber)
	if _, err := c.ModifyLaunchTemplate(ctx, &ec2.ModifyLaunchTemplateInput{
		LaunchTemplateId: aws.String(p.LaunchTemplateID),
		DefaultVersion:   aws.String(strconv.FormatInt(newVersion, 10)),
	}); err != nil {
		return fmt.Errorf("set default launch template version: %w", err)
	}
	p.LaunchTemplateVersion = int(newVersion)
	return nil
}

// MergeTags layers tag maps left-to-right -- later layers override
// earlier ones on key conflict.
// Cascade order in this codebase is project -> pool -> repo (project tags broadest, repo tags most-specific).
// Used both at LT-materialize time (no repo layer
// since one LT serves many repos) and at orchestrator spawn time (full three-layer merge).
// Keep callers funneling through this helper so the precedence stays consistent.
func MergeTags(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, layer := range layers {
		for k, v := range layer {
			out[k] = v
		}
	}
	return out
}

func validateAMI(ctx context.Context, c *ec2.Client, p *pool.Pool) (*ec2types.Image, error) {
	out, err := c.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{p.AMIID}})
	if err != nil {
		return nil, fmt.Errorf("describe ami %s: %w", p.AMIID, err)
	}
	if len(out.Images) == 0 {
		return nil, fmt.Errorf("ami %s not found in this region", p.AMIID)
	}
	return &out.Images[0], nil
}

// validateRootVolume rejects a configured root volume size smaller
// than the AMI's own snapshot size -- EC2 won't launch in that case.
// A zero RootVolumeGB means "use the AMI's native size", which we
// pass through unchanged (buildLTData skips the BlockDeviceMappings
// override when size is zero).
// Returning the AMI size in the error helps the operator fix the form
// without consulting the EC2 console.
func validateRootVolume(p *pool.Pool, ami *ec2types.Image) error {
	if p.RootVolumeGB <= 0 {
		return nil
	}
	rootDev := aws.ToString(ami.RootDeviceName)
	if rootDev == "" {
		return nil
	}
	for _, m := range ami.BlockDeviceMappings {
		if aws.ToString(m.DeviceName) != rootDev || m.Ebs == nil {
			continue
		}
		amiSize := int(aws.ToInt32(m.Ebs.VolumeSize))
		if amiSize > 0 && p.RootVolumeGB < amiSize {
			return fmt.Errorf("root_volume_gb=%d is smaller than the AMI's root volume (%dGB); set 0 to inherit the AMI default or raise the size", p.RootVolumeGB, amiSize)
		}
		break
	}
	return nil
}

func validateNetworking(ctx context.Context, c *ec2.Client, p *pool.Pool) error {
	if _, err := c.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: p.SubnetIDs}); err != nil {
		return fmt.Errorf("describe subnets: %w", err)
	}
	if _, err := c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: p.SecurityGroupIDs}); err != nil {
		return fmt.Errorf("describe security groups: %w", err)
	}
	return nil
}

// validateIAMInstanceProfile checks that the pool's IAMInstanceProfile
// names a real instance profile.
// Two failure modes are distinguished:
//
//   - NoSuchEntity      -- profile doesn't exist; fail fast with a
//     readable error so the operator fixes the
//     form rather than seeing the cryptic EC2
//     "Invalid IAM Instance Profile name" later.
//   - AccessDenied      -- the orchestrator role lacks
//     iam:GetInstanceProfile.
//
// Treat as soft-pass:
//
//	log via the returned error wrapper-style and
//	continue.  Adding the perm is the correct fix.
//	Refusing pool save would force every existing operator to update IAM before the
//	tool works at all.
//
// iamc == nil short-circuits (aws.disabled mode, or callers that can't
// supply a client).
func validateIAMInstanceProfile(ctx context.Context, iamc *iam.Client, p *pool.Pool) error {
	if iamc == nil || p.IAMInstanceProfile == "" {
		return nil
	}
	out, err := iamc.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(p.IAMInstanceProfile),
	})
	if err != nil {
		if _, ok := errors.AsType[*iamtypes.NoSuchEntityException](err); ok {
			return fmt.Errorf("iam: instance profile %q not found in this account; create it with `aws iam create-instance-profile --instance-profile-name %s` then `aws iam add-role-to-instance-profile`", p.IAMInstanceProfile, p.IAMInstanceProfile)
		}
		if apiErr, ok := errors.AsType[smithy.APIError](err); ok && apiErr.ErrorCode() == "AccessDenied" {
			// Soft-pass: the orchestrator role isn't allowed to read
			// instance profiles.
			// Pool save proceeds; if the profile
			// is also missing the orchestrator will surface that at
			// RunInstances time as before.
			return nil
		}
		return fmt.Errorf("iam: get instance profile %q: %w", p.IAMInstanceProfile, err)
	}
	if len(out.InstanceProfile.Roles) == 0 {
		return fmt.Errorf("iam: instance profile %q has no role attached; attach with `aws iam add-role-to-instance-profile --instance-profile-name %s --role-name <role>`", p.IAMInstanceProfile, p.IAMInstanceProfile)
	}
	return nil
}

func buildLTData(p *pool.Pool, ami *ec2types.Image, projectName string, projectTags map[string]string) *ec2types.RequestLaunchTemplateData {
	data := &ec2types.RequestLaunchTemplateData{
		ImageId:          aws.String(p.AMIID),
		SecurityGroupIds: p.SecurityGroupIDs,
		// `shutdown -h` from inside the runner's user-data (both the
		// happy path and the bootstrap-error path) must take the
		// instance away, not leave it stopped racking up EBS charges.
		InstanceInitiatedShutdownBehavior: ec2types.ShutdownBehaviorTerminate,
		MetadataOptions: &ec2types.LaunchTemplateInstanceMetadataOptionsRequest{
			HttpTokens:              ec2types.LaunchTemplateHttpTokensStateRequired, // IMDSv2 mandatory
			HttpEndpoint:            ec2types.LaunchTemplateInstanceMetadataEndpointStateEnabled,
			HttpPutResponseHopLimit: aws.Int32(2),
		},
		TagSpecifications: instanceTagSpecs(p, projectName, projectTags),
	}

	// IAM instance profile is optional -- when blank, instances launch
	// without one and workflows lose AWS-API access from the runner
	// host (still fine for self-contained jobs).
	// Setting the field later requires a pool re-save to bump the LT version.
	if p.IAMInstanceProfile != "" {
		data.IamInstanceProfile = &ec2types.LaunchTemplateIamInstanceProfileSpecificationRequest{
			Name: aws.String(p.IAMInstanceProfile),
		}
	}

	// Root volume override only when an explicit size is requested.
	// Device name comes from the AMI so we don't guess /dev/sda1 vs /dev/xvda.
	if p.RootVolumeGB > 0 && aws.ToString(ami.RootDeviceName) != "" {
		data.BlockDeviceMappings = []ec2types.LaunchTemplateBlockDeviceMappingRequest{
			{
				DeviceName: ami.RootDeviceName,
				Ebs: &ec2types.LaunchTemplateEbsBlockDeviceRequest{
					VolumeSize:          aws.Int32(int32(p.RootVolumeGB)),
					VolumeType:          ec2types.VolumeTypeGp3,
					Encrypted:           aws.Bool(true),
					DeleteOnTermination: aws.Bool(true),
				},
			},
		}
	}

	if p.Spot {
		data.InstanceMarketOptions = &ec2types.LaunchTemplateInstanceMarketOptionsRequest{
			MarketType: ec2types.MarketTypeSpot,
			SpotOptions: &ec2types.LaunchTemplateSpotMarketOptionsRequest{
				InstanceInterruptionBehavior: ec2types.InstanceInterruptionBehaviorTerminate,
				SpotInstanceType:             ec2types.SpotInstanceTypeOneTime,
			},
		}
	}
	return data
}

// instanceTagSpecs is the LT's *default* tag set for instances + volumes
// it spawns when used directly (e.g. from the EC2 console).
// At orchestrator-driven RunInstances time it's superseded by per-spawn
// TagSpecifications in `orchestrator.buildTagSpecs` (which adds
// per-job tags on top).
// Including merged user tags here keeps console-triggered launches consistent
// with orchestrator-driven ones.
func instanceTagSpecs(p *pool.Pool, projectName string, projectTags map[string]string) []ec2types.LaunchTemplateTagSpecificationRequest {
	tags := baseTagSet(p, projectName, projectTags)
	return []ec2types.LaunchTemplateTagSpecificationRequest{
		{ResourceType: ec2types.ResourceTypeInstance, Tags: tags},
		{ResourceType: ec2types.ResourceTypeVolume, Tags: tags},
	}
}

// ltSelfTagSpecs tags the launch template resource itself.
// Includes the merged user tags so cost-allocation queries that filter by
// user tags surface the LT alongside its instances + volumes.
func ltSelfTagSpecs(p *pool.Pool, projectName string, projectTags map[string]string) []ec2types.TagSpecification {
	return []ec2types.TagSpecification{
		{
			ResourceType: ec2types.ResourceTypeLaunchTemplate,
			Tags:         baseTagSet(p, projectName, projectTags),
		},
	}
}

// baseTagSet is the gha:* tool taxonomy plus the merged user tags
// (project tags overlaid by pool tags).
// Used by both the LT resource itself and the LT's default instance/volume tag specs,
// so console-spawn from the LT inherits the same shape the
// orchestrator stamps at RunInstances time (minus the per-job
// tags, which only the orchestrator knows).
func baseTagSet(p *pool.Pool, projectName string, projectTags map[string]string) []ec2types.Tag {
	tags := []ec2types.Tag{
		{Key: aws.String(ManagedByTagKey), Value: aws.String(ManagedByTagValue)},
		{Key: aws.String("gha:project"), Value: aws.String(projectName)},
		{Key: aws.String("gha:pool"), Value: aws.String(p.Name)},
	}
	for k, v := range MergeTags(projectTags, p.Tags) {
		tags = append(tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return tags
}

// ltNameFor derives the EC2 launch template name from project + pool names.
// Project rename does NOT rename the LT - the
// LaunchTemplateId remains stable; the name in the EC2 console
// reflects whatever it was at creation.
//
// AWS rejects launch template names that aren't 3-128 chars or
// contain anything outside `[A-Za-z0-9-()./_]`.
// We sanitize the raw project + pool names to a strict subset (`[A-Za-z0-9._-]`)
// so a project called "My App" or a pool called "x86_64 / large"
// still produces a valid LT name.
// The result is also truncated to fit AWS's 128-char cap.
func ltNameFor(p *pool.Pool, projectName string) string {
	const (
		prefix = "pacer-"
		maxLen = 128
	)
	name := prefix + sanitizeLTName(projectName) + "-" + sanitizeLTName(p.Name)
	if len(name) > maxLen {
		name = name[:maxLen]
	}
	return name
}

// sanitizeLTName collapses runs of disallowed runes to a single dash
// and trims trailing dashes.
// The accepted charset is a strict subset of what AWS allows -- we drop `()` and `/` because they're
// unusual in identifiers and have special meaning to URL parsers.
func sanitizeLTName(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
