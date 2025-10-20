# Copyright (c) 2025, Frappe Cloud and contributors
# For license information, please see license.txt

# import frappe
from frappe.model.document import Document


class ProxyRedirectRule(Document):
	# begin: auto-generated types
	# This code is auto-generated. Do not modify anything in this block.

	from typing import TYPE_CHECKING

	if TYPE_CHECKING:
		from frappe.types import DF

		current_state: DF.Literal["Draft", "Creating", "Active", "Updating", "Deleting"]
		desired_state: DF.Data | None
		domain: DF.Data
		host_redirect: DF.Data | None
		is_tls: DF.Check
		listener_bind_ip: DF.Data
		listener_port: DF.Int
		path_redirect: DF.Data | None
		previous_state: DF.Data
		priority: DF.Int
		redirect_status_code: DF.Int
		route_prefix: DF.Data
		scheme_redirect: DF.Literal["", "HTTP", "HTTPS"]
	# end: auto-generated types

	pass
