// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin

// Promise-based confirmation dialog. One <ConfirmDialog /> instance
// mounts at the app root (in +layout.svelte) and reads the state
// exported here. Anywhere in the app:
//
//   import { confirmDialog } from "$lib/confirm.svelte.js".
//   If (!(await confirmDialog({ title: "...", message: "..." }))) return.
//
// Replaces the browser's blocking window.confirm(), which can't be
// styled and steals focus from the SPA shell.

let state = $state({
  open: false,
  title: "",
  message: "",
  confirmLabel: "Confirm",
  cancelLabel: "Cancel",
  // True = render the confirm button with the danger variant.
  // Defaults true because every existing call site is a delete /
  // unbind / destructive action.
  confirmDanger: true,
  // Internal: set by confirmDialog(), invoked by accept()/cancel(),
  // then cleared so a stale resolve can't fire twice.
  _resolve: null,
});

// Read by ConfirmDialog.svelte. Exported as a function (rather than
// the bare `state`) so the module's $state remains encapsulated --
// callers can't mutate it directly. They go through confirmDialog /
// accept / cancel.
export function confirmState() {
  return state;
}

export function confirmDialog(opts = {}) {
  return new Promise((resolve) => {
    state.title = opts.title || "Confirm";
    state.message = opts.message || "";
    state.confirmLabel = opts.confirmLabel || "Confirm";
    state.cancelLabel = opts.cancelLabel || "Cancel";
    state.confirmDanger = opts.confirmDanger !== false;
    state.open = true;
    state._resolve = resolve;
  });
}

// Called by ConfirmDialog when the user clicks the confirm or cancel
// button (or dismisses via Escape / backdrop click). The resolve
// pointer is captured then nulled so a redundant click during the
// close animation can't double-resolve.
export function settleConfirm(ok) {
  state.open = false;
  const r = state._resolve;
  state._resolve = null;
  if (r) r(ok);
}
