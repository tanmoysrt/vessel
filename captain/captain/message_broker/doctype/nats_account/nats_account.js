// Copyright (c) 2025, Frappe Cloud and contributors
// For license information, please see license.txt

frappe.ui.form.on("NATS Account", {
	refresh(frm) {
		[
			[
				"Revoke Account",
				"revoke_account",
				frm.doc.pending_sync === 0 && frm.doc.revoked === 0,
			],
			[
				"Activate Account",
				"activate_account",
				frm.doc.pending_sync === 0 && frm.doc.revoked === 1,
			],
			["Sync To Remote", "request_sync", frm.doc.pending_sync === 0],
		].forEach(([label, method, condition]) => {
			if (condition) {
				frm.add_custom_button(label, () => {
					frappe.confirm(`Are you sure you want to ${label.toLowerCase()}"?`, () =>
						frm.call(method).then((r) => frm.refresh())
					);
				});
			}
		});
	},
});
