"""Optional exception base for services that want a shared marker type."""


class ContextualError(Exception):
    """Optional base for domain exceptions that carry context through chaining."""
