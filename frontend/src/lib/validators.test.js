// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

import { describe, expect, it } from "vitest";
import {
  AMI_RE,
  POOL_NAME_RE,
  POSIX_USER_RE,
  SG_RE,
  SUBNET_RE,
  fieldErrorsFrom,
  isPoolName,
  isPosixUser,
  isRepoFullName,
  isReservedTagKey,
  isRunnerLabel,
  isRunnerLabelStrict,
  noSlashOrSpace,
  notSelfHosted,
  sanitizeLabel,
} from "./validators.js";

describe("AWS ID regexes", () => {
  it("AMI accepts 8 and 17 hex chars", () => {
    expect(AMI_RE.test("ami-12345678")).toBe(true);
    expect(AMI_RE.test("ami-0123456789abcdef0")).toBe(true);
  });
  it("AMI rejects non-hex / wrong prefix / too short", () => {
    expect(AMI_RE.test("AMI-12345678")).toBe(false); // case
    expect(AMI_RE.test("ami-1234567")).toBe(false); // too short
    expect(AMI_RE.test("ami-XYZ12345")).toBe(false); // non-hex
    expect(AMI_RE.test("foo-12345678")).toBe(false);
  });
  it("subnet + sg patterns mirror AMI", () => {
    expect(SUBNET_RE.test("subnet-12345678")).toBe(true);
    expect(SG_RE.test("sg-12345678")).toBe(true);
    expect(SUBNET_RE.test("subnet-ZZZZZZZZ")).toBe(false);
  });
});

describe("isPoolName / POOL_NAME_RE (runner_label_strict)", () => {
  it("accepts canonical labels", () => {
    expect(isPoolName("large")).toBe(true);
    expect(isPoolName("ci-default")).toBe(true);
    expect(isPoolName("a_b-c_d")).toBe(true);
    expect(isPoolName("123-456")).toBe(true);
    expect(isPoolName("a")).toBe(true);
  });
  it("rejects leading / trailing dash, mixed case, slash", () => {
    expect(isPoolName("-large")).toBe(false);
    expect(isPoolName("large-")).toBe(false);
    expect(isPoolName("Large")).toBe(false);
    expect(isPoolName("with space")).toBe(false);
    expect(isPoolName("owner/repo")).toBe(false);
    expect(isPoolName("")).toBe(false);
  });
});

describe("isReservedTagKey", () => {
  it("flags gha:* keys case-insensitively", () => {
    expect(isReservedTagKey("gha:project")).toBe(true);
    expect(isReservedTagKey("GHA:foo")).toBe(true);
    expect(isReservedTagKey("Gha:")).toBe(true);
  });
  it("allows non-reserved keys", () => {
    expect(isReservedTagKey("team")).toBe(false);
    expect(isReservedTagKey("cost-center")).toBe(false);
    expect(isReservedTagKey("")).toBe(false);
    expect(isReservedTagKey(null)).toBe(false);
  });
});

describe("isPosixUser / POSIX_USER_RE", () => {
  it("empty passes (optional field)", () => {
    expect(isPosixUser("")).toBe(true);
  });
  it("accepts standard POSIX names", () => {
    expect(isPosixUser("ubuntu")).toBe(true);
    expect(isPosixUser("ec2-user")).toBe(true);
    expect(isPosixUser("_root")).toBe(true);
    expect(isPosixUser("a")).toBe(true);
  });
  it("rejects shell metacharacters and bad first char", () => {
    expect(isPosixUser("nobody;rm -rf /")).toBe(false);
    expect(isPosixUser("1user")).toBe(false); // can't start with digit
    expect(isPosixUser("-user")).toBe(false);
    expect(isPosixUser("user$")).toBe(false);
    expect(isPosixUser("User")).toBe(false); // case
  });
  it("caps at 32 chars", () => {
    expect(POSIX_USER_RE.test("a".repeat(32))).toBe(true);
    expect(POSIX_USER_RE.test("a".repeat(33))).toBe(false);
  });
});

describe("noSlashOrSpace", () => {
  it("accepts bare org logins", () => {
    expect(noSlashOrSpace("acme-inc")).toBe(true);
    expect(noSlashOrSpace("octocat")).toBe(true);
    expect(noSlashOrSpace("")).toBe(true); // omitempty handles required-ness
  });
  it("rejects slashes and whitespace", () => {
    expect(noSlashOrSpace("github.com/acme")).toBe(false);
    expect(noSlashOrSpace("acme inc")).toBe(false);
    expect(noSlashOrSpace("acme\tinc")).toBe(false);
  });
});

describe("isRepoFullName", () => {
  it("requires owner/name with non-empty halves", () => {
    expect(isRepoFullName("octocat/hello-world")).toBe(true);
    expect(isRepoFullName("a/b")).toBe(true);
  });
  it("matches the Go SplitN(s, '/', 2) semantics", () => {
    // Both halves non-empty when split on the first slash --
    // "owner/name/sub" -> ["owner", "name/sub"], both non-empty.
    expect(isRepoFullName("owner/name/sub")).toBe(true);
  });
  it("rejects missing or trailing slash", () => {
    expect(isRepoFullName("octocat")).toBe(false);
    expect(isRepoFullName("/name")).toBe(false);
    expect(isRepoFullName("owner/")).toBe(false);
    expect(isRepoFullName("")).toBe(false);
  });
});

describe("sanitizeLabel", () => {
  it("matches Go pool.SanitizeLabel reference cases", () => {
    expect(sanitizeLabel("")).toBe("");
    expect(sanitizeLabel("my-app")).toBe("my-app");
    expect(sanitizeLabel("MyApp")).toBe("myapp");
    expect(sanitizeLabel("My App")).toBe("my-app");
    expect(sanitizeLabel("octocat/hello.world")).toBe("octocat-hello-world");
    expect(sanitizeLabel("---trim---")).toBe("trim");
    expect(sanitizeLabel("a/////b")).toBe("a-b");
    expect(sanitizeLabel("   spaces   ")).toBe("spaces");
    expect(sanitizeLabel("weird!!chars??")).toBe("weird-chars");
    expect(sanitizeLabel("123-numbers_ok")).toBe("123-numbers_ok");
  });
  it("isRunnerLabelStrict only true when input equals canonical form", () => {
    expect(isRunnerLabelStrict("my-app")).toBe(true);
    expect(isRunnerLabelStrict("MyApp")).toBe(false);
    expect(isRunnerLabelStrict("-my-app")).toBe(false);
    expect(isRunnerLabelStrict("")).toBe(false);
  });
  it("isRunnerLabel only requires non-empty post-sanitize", () => {
    expect(isRunnerLabel("foo")).toBe(true);
    expect(isRunnerLabel("Foo Bar")).toBe(true);
    expect(isRunnerLabel("---")).toBe(false);
    expect(isRunnerLabel("")).toBe(false);
  });
});

describe("notSelfHosted", () => {
  it("rejects strings that sanitize to self-hosted", () => {
    expect(notSelfHosted("self-hosted")).toBe(false);
    expect(notSelfHosted("Self-Hosted")).toBe(false);
    expect(notSelfHosted("self_hosted")).toBe(true); // sanitize keeps underscore
    expect(notSelfHosted("self hosted")).toBe(false); // collapses to self-hosted
  });
  it("allows arbitrary other labels", () => {
    expect(notSelfHosted("gpu")).toBe(true);
    expect(notSelfHosted("arm64")).toBe(true);
  });
});

describe("fieldErrorsFrom", () => {
  it("returns empty map for plain errors", () => {
    expect(fieldErrorsFrom(null)).toEqual({});
    expect(fieldErrorsFrom(new Error("oops"))).toEqual({});
  });
  it("collects field-level errors keyed by field", () => {
    const err = new Error("validation");
    err.fields = [
      { field: "name", rule: "required", message: "name is required" },
      { field: "scope", rule: "oneof", message: "scope must be one of: repo, org" },
    ];
    expect(fieldErrorsFrom(err)).toEqual({
      name: "name is required",
      scope: "scope must be one of: repo, org",
    });
  });
  it("joins multiple errors on the same field with '; '", () => {
    const err = new Error("validation");
    err.fields = [
      { field: "name", rule: "min", message: "name must be at least 1" },
      { field: "name", rule: "max", message: "name must be at most 128" },
    ];
    expect(fieldErrorsFrom(err)).toEqual({
      name: "name must be at least 1; name must be at most 128",
    });
  });
  it("skips entries without a field key", () => {
    const err = new Error("validation");
    err.fields = [{ rule: "decode", message: "bad json" }];
    expect(fieldErrorsFrom(err)).toEqual({});
  });
});
