from functools import wraps


def ensure_state(*allowed_states):
	"""
	Decorator to ensure that an object's current_state is in the allowed states
	before executing a method.

	Usage:
	    @ensure_state('Active', 'Draft')
	    def upsert(self):
	        ...
	"""

	def decorator(method):
		@wraps(method)
		def wrapper(self, *args, **kwargs):
			current = getattr(self, "current_state", None)
			if current not in allowed_states:
				cls_name = self.__class__.__name__
				method_name = method.__name__
				allowed = ", ".join(allowed_states)
				message = (
					f"{cls_name} should be in one of the valid states "
					f"({allowed}) to call '{method_name}'. "
					f"Current status is '{current}'."
				)
				raise ValueError(message)
			return method(self, *args, **kwargs)

		return wrapper

	return decorator


def validate_cert_and_key_pem(cert_pem: str, key_pem: str):
	"""
	Validate a TLS certificate PEM and private key PEM provided as strings.

	Returns:
	    (is_valid: bool, expiry_time_utc: datetime | None)
	"""
	import datetime as dt

	from cryptography import x509
	from cryptography.hazmat.primitives import serialization

	try:
		cert_bytes = cert_pem.encode() if isinstance(cert_pem, str) else cert_pem
		key_bytes = key_pem.encode() if isinstance(key_pem, str) else key_pem

		# Parse cert (PEM first, fallback DER)
		try:
			cert = x509.load_pem_x509_certificate(cert_bytes)
		except ValueError:
			cert = x509.load_der_x509_certificate(cert_bytes)

		# Parse key (PEM first, fallback DER). No passphrase support per request.
		try:
			key = serialization.load_pem_private_key(key_bytes, password=None)
		except ValueError:
			key = serialization.load_der_private_key(key_bytes, password=None)
	except Exception:
		return (False, None)

	# Expiry (tz-aware UTC)
	try:
		expiry = getattr(cert, "not_valid_after_utc", None)
		if expiry is None:
			expiry = cert.not_valid_after.replace(tzinfo=dt.timezone.utc)
		not_before = getattr(cert, "not_valid_before_utc", None)
		if not_before is None:
			not_before = cert.not_valid_before.replace(tzinfo=dt.timezone.utc)
	except Exception:
		return (False, None)

	# Check validity window
	now = dt.datetime.now(dt.timezone.utc)
	if not (not_before <= now <= expiry):
		return (False, expiry)

	# Check key matches cert public key (compare SubjectPublicKeyInfo bytes)
	try:
		cert_spki = cert.public_key().public_bytes(
			serialization.Encoding.DER,
			serialization.PublicFormat.SubjectPublicKeyInfo,
		)
		key_spki = key.public_key().public_bytes(
			serialization.Encoding.DER,
			serialization.PublicFormat.SubjectPublicKeyInfo,
		)
		if cert_spki != key_spki:
			return (False, expiry)
	except Exception:
		return (False, expiry)

	return (True, expiry)
