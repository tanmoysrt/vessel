// Copyright (c) 2025, Frappe Cloud and contributors
// For license information, please see license.txt

frappe.ui.form.on("Proxy TLS Certificate", {
	refresh(frm) {
		[["Delete Rule", "delete", frm.doc.current_state == "Active"]].forEach(
			([label, method, condition]) => {
				if (condition) {
					frm.add_custom_button(label, () => {
						frappe.confirm(`Are you sure you want to ${label.toLowerCase()}?`, () =>
							frm.call(method).then((r) => frm.refresh())
						);
					});
				}
			}
		);
	},
});
