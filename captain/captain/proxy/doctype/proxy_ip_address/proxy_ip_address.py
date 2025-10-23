# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

import frappe
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

	def validate(self):
		if self.has_value_changed("cidr"):
			if (
				self.cidr < 0
				or (self.type == "V4" and self.cidr > 32)
				or (self.type == "V6" and self.cidr > 128)
			):
				frappe.throw(f"Invalid CIDR notation {self.address}/{self.cidr} for IP version {self.type}.")

		if self.has_value_changed("address"):
			import ipaddress

			try:
				ip = ipaddress.ip_address(self.address)
				if (self.type == "V4" and ip.version != 4) or (self.type == "V6" and ip.version != 6):
					frappe.throw(f"IP address {self.address} does not match the specified type {self.type}.")
			except ValueError:
				frappe.throw(f"Invalid IP address: {self.address}")
