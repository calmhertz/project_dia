"""Worker configuration, read from the environment.

Hardware-specific values are explicit and have no default: guessing a serial
port or an SDR gain is a hardware-safety risk (worker-spec section 5).
"""

import os
from dataclasses import dataclass
from pathlib import Path

MIN_SHARED_SECRET_LENGTH = 32


@dataclass(frozen=True)
class Config:
    # The Worker is configured with a name; the Server resolves the
    # identifiers during registration (spec.md section 7).
    worker_name: str

    server_grpc_address: str
    server_shared_secret: str
    server_tls_enabled: bool

    state_directory: Path
    recordings_directory: Path

    rotator_serial_port: str
    rotator_baud_rate: int
    rotator_park_azimuth: int
    rotator_park_elevation: int

    sdr_device_index: int
    rtl_test_binary: str

    satdump_binary: str
    satdump_pipelines_dir: str

    log_level: str
    heartbeat_interval_seconds: int


def load_config() -> Config:
    """Read and validate Worker configuration."""
    shared_secret = _required("AAGASA_WORKER_SHARED_SECRET")
    if len(shared_secret) < MIN_SHARED_SECRET_LENGTH:
        raise ValueError(
            f"AAGASA_WORKER_SHARED_SECRET must be at least {MIN_SHARED_SECRET_LENGTH} characters"
        )

    return Config(
        worker_name=_required("AAGASA_WORKER_NAME"),
        server_grpc_address=_required("AAGASA_SERVER_GRPC_ADDRESS"),
        server_shared_secret=shared_secret,
        server_tls_enabled=_env_bool("AAGASA_SERVER_TLS_ENABLED", True),
        state_directory=Path(_env("AAGASA_STATE_DIR", "/var/lib/aagasa/state")),
        recordings_directory=Path(_env("AAGASA_RECORDINGS_DIR", "/var/lib/aagasa/recordings")),
        rotator_serial_port=_required("AAGASA_ROTATOR_SERIAL_PORT"),
        rotator_baud_rate=_env_int("AAGASA_ROTATOR_BAUD_RATE", 9600),
        rotator_park_azimuth=_env_degrees("AAGASA_ROTATOR_PARK_AZIMUTH", 0, 0, 359),
        rotator_park_elevation=_env_degrees("AAGASA_ROTATOR_PARK_ELEVATION", 0, 0, 90),
        sdr_device_index=_env_index("AAGASA_SDR_DEVICE_INDEX", 0),
        rtl_test_binary=_env("AAGASA_RTL_TEST_BIN", "rtl_test"),
        satdump_binary=_env("AAGASA_SATDUMP_BIN", "satdump"),
        satdump_pipelines_dir=_env("AAGASA_SATDUMP_PIPELINES_DIR", ""),
        log_level=_env("AAGASA_LOG_LEVEL", "info").upper(),
        heartbeat_interval_seconds=_env_int("AAGASA_HEARTBEAT_INTERVAL_SECONDS", 5),
    )


def _required(name: str) -> str:
    value = os.getenv(name, "").strip()
    if not value:
        raise ValueError(f"missing required environment variable: {name}")
    return value


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


def _env_index(name: str, default: int) -> int:
    """Read a zero-based device index."""
    raw = os.getenv(name, "").strip()
    if not raw:
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be an integer") from exc
    if value < 0:
        raise ValueError(f"{name} cannot be negative")
    return value


def _env_degrees(name: str, default: int, low: int, high: int) -> int:
    """Read an angle, refusing values the G-550 cannot accept."""
    raw = os.getenv(name, "").strip()
    if not raw:
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be an integer") from exc
    if not low <= value <= high:
        raise ValueError(f"{name} must be between {low} and {high}")
    return value


def _env_bool(name: str, default: bool) -> bool:
    raw = os.getenv(name, "").strip().lower()
    if not raw:
        return default
    if raw in ("1", "true", "yes"):
        return True
    if raw in ("0", "false", "no"):
        return False
    raise ValueError(f"{name} must be a boolean")
