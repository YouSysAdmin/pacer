// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// adapter-static needs every route to be prerenderable.  No
// server-side data loading here - pages fetch from the API
// client-side after hydration.
export const prerender = true;
export const ssr = false;
