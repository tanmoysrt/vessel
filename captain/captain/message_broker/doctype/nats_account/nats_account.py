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
		parent: DF.Data
		parentfield: DF.Data
		parenttype: DF.Data
		pending_sync: DF.Check
		revoked: DF.Check
	# end: auto-generated types

	def sync(self):
		frappe.db.get_value(self.doctype, self.name, "name", for_update=True)

		nats_settings: NATSSettings = frappe.get_cached_doc("NATS Settings", "NATS Settings")
		if self.revoked:
			nats_settings.nsc.revoke_account(self.account_name)
		else:
			nats_settings.nsc.push_account(self.account_name)

		self.pending_sync = False
		self.save(ignore_permissions=True)
