// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import { audit, auth, jobs, projects, repos, stats } from "./api.js";

// Replace fetch + window.location with mocks each test so we can
// inspect what the wrapper sent without hitting the network or
// triggering a real navigation.
let fetchMock;
let originalLocation;

function jsonResponse(status, body) {
  const text = body == null ? "" : JSON.stringify(body);
  return {
    status,
    ok: status >= 200 && status < 300,
    text: () => Promise.resolve(text),
  };
}

beforeEach(() => {
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);

  // jsdom's window.location is non-configurable; replace by proxying
  // through a fresh object so we can record href assignments.
  originalLocation = window.location;
  delete window.location;
  window.location = {
    pathname: "/projects",
    search: "",
    href: "http://localhost/projects",
  };
});

afterEach(() => {
  window.location = originalLocation;
  vi.unstubAllGlobals();
});

describe("call() response handling", () => {
  it("204 returns null", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(204, null));
    const out = await projects.delete("abc");
    expect(out).toBeNull();
  });

  it("200 with body returns parsed JSON", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [{ id: "p1" }]));
    const out = await projects.list();
    expect(out).toEqual([{ id: "p1" }]);
  });

  it("200 with empty body returns null", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, null));
    const out = await projects.list();
    expect(out).toBeNull();
  });

  it("non-2xx with {error} envelope throws that message", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(400, { error: "name required" }));
    await expect(projects.create({ name: "" })).rejects.toThrow("name required");
  });

  it("non-2xx with non-JSON body throws HTTP <status>", async () => {
    fetchMock.mockResolvedValueOnce({
      status: 503,
      ok: false,
      text: () => Promise.resolve("Service Unavailable"),
    });
    await expect(projects.list()).rejects.toThrow("HTTP 503");
  });
});

describe("call() 401 handling", () => {
  it("401 on protected path redirects to /login with next= encoded", async () => {
    window.location.pathname = "/pools";
    window.location.search = "?status=running";
    fetchMock.mockResolvedValueOnce(jsonResponse(401, { error: "unauthorized" }));

    await expect(jobs.list("running")).rejects.toThrow(/redirecting/);
    expect(window.location.href).toBe(
      "/login?next=" + encodeURIComponent("/pools?status=running")
    );
  });

  it("401 on /api/auth/* does NOT redirect; throws envelope", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(401, { error: "bad password" }));
    const before = window.location.href;
    await expect(auth.login("a@b", "x")).rejects.toThrow("bad password");
    expect(window.location.href).toBe(before);
  });

  it("401 while already on /login does NOT redirect", async () => {
    window.location.pathname = "/login";
    fetchMock.mockResolvedValueOnce(jsonResponse(401, { error: "session expired" }));
    const before = window.location.href;
    await expect(projects.list()).rejects.toThrow("session expired");
    expect(window.location.href).toBe(before);
  });
});

describe("query-string builders", () => {
  it("audit.list() omits unset params and includes 0 offset", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { entries: [], total: 0 }));
    await audit.list({ limit: 50, offset: 0, action: "project.created" });
    const url = fetchMock.mock.calls[0][0];
    expect(url).toMatch(/^\/api\/audit\?/);
    expect(url).toContain("limit=50");
    expect(url).toContain("offset=0");
    expect(url).toContain("action=project.created");
    expect(url).not.toContain("since=");
    expect(url).not.toContain("actor=");
  });

  it("audit.list() with no args has no qs", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { entries: [], total: 0 }));
    await audit.list();
    expect(fetchMock.mock.calls[0][0]).toBe("/api/audit");
  });

  it("jobs.list() with no status omits status= but keeps default limit", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, []));
    await jobs.list();
    const url = fetchMock.mock.calls[0][0];
    expect(url).toContain("limit=50");
    expect(url).not.toContain("status=");
  });

  it("stats.rollup() includes only set fields", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, {}));
    await stats.rollup({ from: "2026-01-01", groupBy: "project" });
    const url = fetchMock.mock.calls[0][0];
    expect(url).toContain("from=2026-01-01");
    expect(url).toContain("group_by=project");
    expect(url).not.toContain("to=");
  });
});

describe("request bodies + methods", () => {
  it("projects.create sends POST with JSON body + Content-Type header", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(201, { id: "p1" }));
    await projects.create({ name: "demo" });

    const [, opts] = fetchMock.mock.calls[0];
    expect(opts.method).toBe("POST");
    expect(JSON.parse(opts.body)).toEqual({ name: "demo" });
    expect(opts.headers["Content-Type"]).toBe("application/json");
    expect(opts.credentials).toBe("same-origin");
  });

  it("repos.unbind keeps slash literal in path", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(204, null));
    await repos.unbind("octocat/hello-world");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/repos/octocat/hello-world");
    expect(fetchMock.mock.calls[0][1].method).toBe("DELETE");
  });
});
