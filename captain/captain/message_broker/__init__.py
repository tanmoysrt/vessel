from typing import TYPE_CHECKING

import frappe

from .nats import NATSBackgroundMessageProcessor, NatsClient, run_message_processor
from .nsc import NSC

if TYPE_CHECKING:
	from captain.message_broker.doctype.nats_message.nats_message import NATSMessage


def publish_message(subject: str, payload: str) -> "NATSMessage":
	return frappe.get_doc(
		{
			"doctype": "NATS Message",
			"direction": "Outgoing",
			"subject": subject,
			"payload": payload,
		}
	).insert(ignore_permissions=True)


__all__ = ["NSC", "NATSBackgroundMessageProcessor", "NatsClient", "publish_message", "run_message_processor"]
