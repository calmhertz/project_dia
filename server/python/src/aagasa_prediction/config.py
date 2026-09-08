"""Configuration for the prediction service, read from the environment."""

import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Config:
    address: str
    log_level: str
    max_workers: int


def load_config() -> Config:
    """Read configuration from the environment, applying safe defaults.

    The service is internal to the deployment, so it binds loopback unless the
    deployment explicitly widens it.
    """
    return Config(
        address=_env("AAGASA_PREDICTION_ADDRESS", "127.0.0.1:9091"),
        log_level=_env("AAGASA_LOG_LEVEL", "info").upper(),
        max_workers=_env_int("AAGASA_PREDICTION_MAX_WORKERS", 8),
    )


def _env(name: str, default: str) -> str:
    value = os.getenv(name, "").strip()
    return value or default


def _env_int(name: str, default: int) -> int:
    raw = os.getenv(name, "").strip()
    if not raw:
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be an integer") from exc
    if value <= 0:
        raise ValueError(f"{name} must be positive")
    return value
