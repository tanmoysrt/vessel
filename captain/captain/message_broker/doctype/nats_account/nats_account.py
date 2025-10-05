# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

from typing import TYPE_CHECKING

import frappe
from frappe.model.document import Document

if TYPE_CHECKING:
	from captain.message_broker.doctype.nats_settings.nats_settings import NATSSettings


class NATSAccount(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		account_id: DF.Data | None
		account_name: DF.Data
		pending_sync: DF.Check
		revoked: DF.Check
	# end: auto-generated types

	def before_insert(self):
		invalid_chars = set(" @!#$%^&*()+=[]{}|\\;:'\",<>/?.")
		if any((char in invalid_chars) for char in self.account_name):
			frappe.throw(
				"Account Name cannot contain spaces or special characters: " + " ".join(invalid_chars)
			)

	@frappe.whitelist()
	def request_sync(self):
		frappe.db.get_value(self.doctype, self.name, "name", for_update=True)

		self.pending_sync = True
		self.save(ignore_permissions=True)

	def sync(self):
		frappe.db.get_value(self.doctype, self.name, "name", for_update=True)

		nats_settings: NATSSettings = frappe.get_doc("NATS Settings", "NATS Settings")
		if self.revoked:
			nats_settings.nsc.revoke_account(self.account_name)
		else:
			nats_settings.nsc.push_account(self.account_name)

		self.pending_sync = False
		self.save(ignore_permissions=True)

	@frappe.whitelist()
	def activate(self):
		if not self.revoked:
			frappe.throw("Account is already active")

		frappe.db.get_value(self.doctype, self.name, "name", for_update=True)

		self.revoked = False
		self.pending_sync = True
		self.save()
		frappe.msgprint("Account will be activated shortly.")

	@frappe.whitelist()
	def revoke(self):
		if self.revoked:
			frappe.throw("Account is already revoked")

		frappe.db.get_value(self.doctype, self.name, "name", for_update=True)

		self.revoked = True
		self.pending_sync = True
		self.save()
		frappe.msgprint("Account and all user access will be revoked shortly.")

	def on_trash(self):
		frappe.throw("NATS Account records cannot be deleted. You can revoke the account instead.")
