import click
import frappe
from frappe.commands import get_site, pass_context


@click.command("run-nats-message-processor")
@pass_context
def run_nats_message_processor(context):
	from captain.message_broker.nats import run_message_processor

	site = get_site(context)

	try:
		frappe.init(site)
		frappe.connect()

		run_message_processor()
	finally:
		frappe.destroy()


commands = [run_nats_message_processor]
