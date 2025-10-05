// Copyright (c) 2025, Frappe Cloud and contributors
// For license information, please see license.txt

frappe.ui.form.on("NATS Settings", {
	refresh(frm) {
		[
			["Initialize NSC", "init", frm.doc.is_nsc_initialized === 0],
			["Sync Info", "sync_info", frm.doc.is_nsc_initialized === 1],
			[
				"Show NATS Server Config",
				"show_nats_server_config",
				frm.doc.is_nsc_initialized === 1,
			],
		].forEach(([label, method, condition]) => {
			if (condition) {
				frm.add_custom_button(
					label,
					() => {
						frappe.confirm(`Are you sure you want to ${label.toLowerCase()}?`, () =>
							frm.call(method).then((r) => frm.refresh())
						);
					},
					"Actions"
				);
			}
		});

		[
			["Add Account", "add_account", frm.doc.is_nsc_initialized === 1],
			["Revoke Account", "revoke_account", frm.doc.is_nsc_initialized === 1],
			["Activate Account", "activate_account", frm.doc.is_nsc_initialized === 1],
		].forEach(([label, method, condition]) => {
			if (condition) {
				frm.add_custom_button(
					label,
					() => {
						frappe.prompt(
							[
								{
									label: "Account Name",
									fieldname: "account_name",
									fieldtype: "Data",
									reqd: 1,
								},
							],
							(values) => {
								frappe.confirm(
									`Are you sure you want to ${label.toLowerCase()} "${
										values.account_name
									}"?`,
									() =>
										frm
											.call(method, {
												account_name: values.account_name,
											})
											.then((r) => frm.refresh())
								);
							},
							label
						);
					},
					"Manage Accounts"
				);
			}
		});
	},
});
