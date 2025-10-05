import contextlib
import os
import subprocess
from functools import wraps
from typing import Literal

import filelock


def with_global_lock():
	def decorator(func):
		@wraps(func)
		def wrapper(self, *args, **kwargs):
			with self.global_lock():
				return func(self, *args, **kwargs)

		return wrapper

	return decorator


class NSC:
	# Initialization Methods
	# -----------------------
	def __init__(self, nsc_directory: str, operator: str) -> None:
		self.nsc_directory = nsc_directory
		self._locks_directory = os.path.join(nsc_directory, "locks")
		self._operator_lock_file = os.path.join(self._locks_directory, "operator.lock")
		self.operator = operator
		self.sys_account = "SYS"
		self.sys_user = "sys"
		self._global_lock = None

	def init(self):
		if not os.path.exists(self.nsc_directory):
			raise FileNotFoundError(f"NSC Directory {self.nsc_directory} does not exist")

		# Check if nsc directory is empty
		if self.is_initialized():
			raise Exception(
				f"NSC Directory {self.nsc_directory} is not empty. NSC seems to be already initialized."
			)

		# Create locks directory if it doesn't exist
		os.makedirs(self._locks_directory, exist_ok=True)

		try:
			# Initialize NSC
			self._run_nsc_command(
				[
					"add",
					"operator",
					self.operator,
					"--sys",
				],
				set_operator=False,
			)

			# Create account with same name as operator
			self.add_account(self.operator, sync=False)

		except Exception as e:
			self.cleanup()
			raise e

	def is_initialized(self) -> bool:
		if not os.path.exists(self.nsc_directory):
			return False
		files = os.listdir(self.nsc_directory)
		# Filter out .gitignore and lock folder
		files = [f for f in files if f not in [".gitignore", "locks"]]
		if files:
			return True
		return False

	# Operator Methods
	# -----------------------
	@with_global_lock()
	def set_account_jwt_server_url(self, url: str):
		self._run_nsc_command(["edit", "operator", "--account-jwt-server-url", url])

	# Account CRUD Methods
	# -----------------------
	@with_global_lock()
	def add_account(self, account_name: str, sync: bool = True):
		account_jwt_path = self.get_jwt_path("account", account_name)
		if os.path.exists(account_jwt_path):
			return self.get_jwt_dict("account", account_name).get("sub")

		try:
			self._run_nsc_command(["add", "account", account_name])
			self._run_nsc_command(
				[
					"edit",
					"account",
					"--name",
					account_name,
					'--allow-pubsub=">"',
					"--allow-pub-response=-1",
					"--js-enable=0",
				]
			)

			# Create user with same name as admin account
			self.add_user(account_name, account_name, pub=[">"], sub=[">"])

			if sync:
				self.push_account(account_name)
			return self.get_jwt_dict("account", account_name).get("sub")
		except Exception as e:
			self.delete_account(account_name)
			raise Exception(f"Failed to create account {account_name}") from e

	def is_exist_account(self, account_name: str) -> bool:
		try:
			self._run_nsc_command(["describe", "account", account_name])
			return True
		except Exception:
			return False

	@with_global_lock()
	def push_account(self, account_name: str):
		self._run_nsc_command(["push", "-a", account_name])

	@with_global_lock()
	def revoke_account(self, account_name: str):
		self._run_nsc_command(["push", "-R", account_name])

	@with_global_lock()
	def delete_account(self, account_name: str) -> bool:
		account_jwt_path = self.get_jwt_path("account", account_name)
		if not os.path.exists(account_jwt_path):
			return True

		try:
			self._run_nsc_command(["push", "-R", account_name])
			self._run_nsc_command(["delete", "account", account_name])
			return True
		except Exception:
			return False

	# User CRUD Methods
	# -----------------------
	def add_user(
		self, account_name: str, user_name: str, pub: list[str] | None = None, sub: list[str] | None = None
	) -> str | None:
		# Check if account exists
		if not self.is_exist_account(account_name):
			raise Exception(f"Account {account_name} does not exist")

		try:
			# Create user only if it doesn't exist
			if not self.is_exist_user(account_name, user_name):
				self._run_nsc_command(
					["add", "user", user_name, "--account", account_name, "--allow-pub-response=-1"]
				)  # enable pub response by default

			# Update user permissions
			self.update_user_permissions(account_name, user_name, pub, sub)

			# Find user id from jwt
			user_jwt_info = self.get_jwt_dict("user", account_name, user_name)
			user_id = user_jwt_info.get("sub")
			return user_id

		except Exception as e:
			with contextlib.suppress(Exception):
				self.delete_user(account_name, user_name, revoke=False)

			raise e

	def update_user_permissions(
		self, account_name: str, user_name: str, pub: list[str] | None = None, sub: list[str] | None = None
	):
		if pub is None:
			pub = []
		if sub is None:
			sub = []

		user_data = self.get_jwt_dict("user", account_name, user_name)
		nats_info = user_data.get("nats", {})
		current_allowed_pub = nats_info.get("pub", {}).get("allow", [])
		current_denied_pub = nats_info.get("pub", {}).get("deny", [])
		current_allowed_sub = nats_info.get("sub", {}).get("allow", [])
		current_denied_sub = nats_info.get("sub", {}).get("deny", [])

		all_pub_sub = set(
			current_allowed_pub + current_denied_pub + current_allowed_sub + current_denied_sub + pub + sub
		)

		# Remove all existing subject from any rule
		if all_pub_sub:
			self._run_nsc_command(
				[
					"edit",
					"user",
					user_name,
					"-a",
					account_name,
					"--rm",
					",".join(all_pub_sub),
				]
			)

		if not pub and not sub:
			return

		# Add new rules
		cmd = [
			"edit",
			"user",
			user_name,
			"-a",
			account_name,
		]

		if pub:
			cmd += ["--allow-pub", ",".join(pub)]
		if sub:
			cmd += ["--allow-sub", ",".join(sub)]

		self._run_nsc_command(cmd)

	def delete_user(self, account_name: str, user_name: str, revoke: bool = True):
		user_jwt_path = self.get_jwt_path("user", account_name=account_name, user_name=user_name)
		if not os.path.exists(user_jwt_path):
			return

		cmd = [
			"delete",
			"user",
			user_name,
			"--account",
			account_name,
			"--rm-creds",
			"--rm-nkey",
		]
		if revoke:
			cmd.append("--revoke")

		self._run_nsc_command(cmd)

	def revoke_user(self, account_name: str, user_name: str):
		user_jwt_path = self.get_jwt_path("user", account_name=account_name, user_name=user_name)
		if not os.path.exists(user_jwt_path):
			return

		user_jwt_info = self.get_jwt_dict("user", account_name, user_name)
		user_id = user_jwt_info.get("sub")
		if not user_id:
			return

		self._run_nsc_command(
			["revocations", "add-user", "--account", account_name, "--user-public-key", user_id]
		)

	def is_exist_user(self, account_name: str, user_name: str) -> bool:
		try:
			self._run_nsc_command(["describe", "user", user_name, "--account", account_name])
			return True
		except Exception:
			return False

	def remove_user_revocation(self, account_name: str, user_name: str):
		user_jwt_path = self.get_jwt_path("user", account_name=account_name, user_name=user_name)
		if not os.path.exists(user_jwt_path):
			return

		user_jwt_info = self.get_jwt_dict("user", account_name, user_name)
		user_id = user_jwt_info.get("sub")
		if not user_id:
			return

		self._run_nsc_command(
			["revocations", "delete-user", "--account", account_name, "--user-public-key", user_id]
		)

	# JWT / Config Methods
	# -----------------------
	def get_jwt_path(
		self,
		entity_type: Literal["operator", "account", "user"],
		account_name: str | None = None,
		user_name: str | None = None,
	) -> str:
		if entity_type != "operator" and not account_name:
			raise ValueError("Name must be provided for non-operator entity types")

		if entity_type == "user" and not user_name:
			raise ValueError("User name must be provided for user entity type")

		operator_folder = os.path.join(self.nsc_directory, self.operator)

		if entity_type == "operator":
			jwt_file_path = os.path.join(operator_folder, f"{self.operator}.jwt")
		elif entity_type == "account":
			jwt_file_path = os.path.join(operator_folder, "accounts", account_name, f"{account_name}.jwt")
		elif entity_type == "user":
			jwt_file_path = os.path.join(
				operator_folder, "accounts", account_name, "users", f"{user_name}.jwt"
			)
		else:
			raise ValueError(f"Invalid entity type: {entity_type}")
		return jwt_file_path

	def get_jwt_dict(
		self,
		entity_type: Literal["operator", "account", "user"],
		account_name: str | None = None,
		user_name: str | None = None,
	) -> dict:
		import jwt

		jwt_file_path = self.get_jwt_path(entity_type, account_name, user_name)
		with open(jwt_file_path) as f:
			jwt_content = f.read()
		return jwt.decode(jwt_content, options={"verify_signature": False})

	def get_user_credential(
		self,
		account_name: str,
		user_name: str,
	):
		path = self.get_user_credential_path(account_name, user_name)
		if not os.path.exists(path):
			raise FileNotFoundError(f"Creds file for user {user_name} in account {account_name} not found")

		with open(path) as f:
			return f.read()

	def get_user_credential_path(
		self,
		account_name: str,
		user_name: str,
	) -> str:
		if not account_name:
			raise ValueError("Account name must be provided for creds file")

		if not user_name:
			raise ValueError("User name must be provided for creds file")

		creds_file_path = os.path.join(
			self.nsc_directory, "creds", self.operator, account_name, f"{user_name}.creds"
		)
		return creds_file_path

	def generate_nats_server_config(self) -> str:
		import jwt

		with open(os.path.join(self.nsc_directory, self.operator, f"{self.operator}.jwt")) as f:
			operator_jwt = f.read()
		operator_jwt_decoded = self.get_jwt_dict("operator")
		operator_jwt_system_account_identifier = operator_jwt_decoded.get("nats", {}).get("system_account")

		with open(
			os.path.join(
				self.nsc_directory, self.operator, "accounts", self.sys_account, f"{self.sys_account}.jwt"
			)
		) as f:
			system_account_jwt = f.read()

		system_account_jwt_decoded = jwt.decode(system_account_jwt, options={"verify_signature": False})
		system_account_public_key = system_account_jwt_decoded.get("sub")

		if not system_account_public_key:
			raise Exception("Failed to find system account public key")

		if operator_jwt_system_account_identifier != system_account_public_key:
			raise Exception("Operator's system account does not match the system account public key")

		return f"""
operator: {operator_jwt}

system_account: {system_account_public_key}

resolver {{
    type: full
    dir: '/data/jwt'
    allow_delete: true
    interval: "2m"
    timeout: "1.9s"
}}

resolver_preload: {{
	{system_account_public_key}: {system_account_jwt},
}}
"""

	# Utility / Internal
	# ------------------
	def _run_nsc_command(self, args: list[str], set_operator: bool = True):
		if set_operator:
			response = subprocess.run(
				["nsc", "select", "operator", self.operator, "--all-dirs", self.nsc_directory],
				check=False,
				capture_output=True,
				text=True,
			)
			if response.returncode != 0:
				raise Exception(f"Failed to select operator {self.operator}: {response.stderr.strip()}")

		response = subprocess.run(
			["nsc", *args, "--all-dirs", self.nsc_directory],
			check=False,
			capture_output=True,
			text=True,
		)
		if response.returncode != 0:
			raise Exception(response.stderr.strip())

		return response.stdout.strip()

	@contextlib.contextmanager
	def global_lock(self):
		if self._global_lock is None:
			self._global_lock = filelock.FileLock(os.path.join(self._locks_directory, "global.lock"))

		with self._global_lock:
			yield

	def cleanup(self):
		import shutil

		with contextlib.suppress(Exception):
			if os.path.exists(self.nsc_directory):
				shutil.rmtree(self.nsc_directory)

			os.makedirs(self.nsc_directory, exist_ok=True)
