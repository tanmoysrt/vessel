# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

# import frappe
from frappe.model.document import Document


class ProxyIPAddress(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		address: DF.Data
		cidr: DF.Int
		parent: DF.Data
		parentfield: DF.Data
		parenttype: DF.Data
		type: DF.Literal["V4", "V6"]
	# end: auto-generated types

	pass
