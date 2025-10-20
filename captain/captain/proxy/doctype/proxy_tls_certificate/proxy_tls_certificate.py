# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

# import frappe
from frappe.model.document import Document


class ProxyTLSCertificate(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		certificate_pem: DF.LongText
		current_state: DF.Literal["Draft", "Creating", "Active", "Updating", "Deleting"]
		desired_status: DF.Data | None
		domain: DF.Data
		expiry_time: DF.Datetime | None
		is_wildcard: DF.Check
		previoust_state: DF.Data | None
		private_key_pem: DF.LongText
	# end: auto-generated types

	pass
