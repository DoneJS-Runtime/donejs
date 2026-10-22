"""DonePM error types."""

from __future__ import annotations


class DonePMError(Exception):
    """Base class for every DonePM failure."""


class UsageError(DonePMError):
    """Bad invocation."""


class SecurityError(DonePMError):
    """A command was refused by the security policy."""


class PermissionDenied(SecurityError):
    """The user did not (or could not) approve an action."""


class BlockedCommand(SecurityError):
    """The command matches a hard-block rule and is never run."""


class RegistryError(DonePMError):
    """The registry returned an error or an unusable payload."""


class PackageError(DonePMError):
    """A package manifest is malformed or inconsistent."""


class LockfileError(DonePMError):
    """A package-lock.json is malformed."""
