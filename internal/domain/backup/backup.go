// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Package backup ships configuration in and out as a single JSON
// document - projects, their pools, and their repo bindings. Operational
// data (jobs, instances, audit log, users, secrets) is intentionally
// out of scope: this is for restoring the SHAPE of a deployment after
// upgrade / migration / disaster, not its runtime state.
//
// Identity for upsert is by name: projects by Project.Name, pools by
// (Project.Name, Pool.Name), repos by Repo.FullName. UUIDs are not
// carried across systems - imports look up by stable name and either
// update the existing row in place (preserving its ID + CreatedAt) or
// create a fresh row with a new UUID.
//
// Pool import re-materializes the EC2 launch template via the same
// ec2lt.CreateOrUpdate path the regular pool save uses. Existing pools
// bump LT version; new pools allocate a fresh LT. In aws.disabled dev
// mode the AWS leg is skipped exactly like the pool handler does.
package backup
