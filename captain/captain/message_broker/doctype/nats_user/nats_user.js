// Copyright (c) 2025, Frappe Cloud and contributors
// For license information, please see license.txt

frappe.ui.form.on("NATS User", {
	refresh(frm) {
		[
			["Request Revocation", "request_revocation", frm.doc.status === "Active"],
			["Revert Revocation", "revert_revocation", frm.doc.status === "Revoked"],
		].forEach(([label, method, condition]) => {
			if (condition) {
				frm.add_custom_button(label, () => {
					frappe.confirm(`Are you sure you want to ${label.toLowerCase()}?`, () =>
						frm.call(method).then((r) => frm.refresh())
					);
				});
			}
		});

		frm.add_custom_button("Show Credential", () => {
			frm.call("get_credential").then((r) => {
				frappe.msgprint("<pre>" + r.message + "</pre>", "Credential");
			});
		});
	},
});
