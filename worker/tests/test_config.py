"""Worker configuration validation tests."""

import pytest

from aagasa_worker.config import load_config

VALID_SECRET = "0123456789abcdef0123456789abcdef"

REQUIRED = {
    "AAGASA_WORKER_NAME": "worker-1",
    "AAGASA_SERVER_GRPC_ADDRESS": "server.example:9090",
    "AAGASA_WORKER_SHARED_SECRET": VALID_SECRET,
    "AAGASA_ROTATOR_SERIAL_PORT": "/dev/ttyUSB0",
}


@pytest.fixture
def valid_env(monkeypatch):
    for name, value in REQUIRED.items():
        monkeypatch.setenv(name, value)
    for name in ("AAGASA_SERVER_TLS_ENABLED", "AAGASA_ROTATOR_BAUD_RATE", "AAGASA_STATE_DIR"):
        monkeypatch.delenv(name, raising=False)
    return monkeypatch


def test_defaults(valid_env):
    config = load_config()
    assert config.rotator_baud_rate == 9600
    assert config.heartbeat_interval_seconds == 5
    # TLS must be on unless the deployment explicitly disables it.
    assert config.server_tls_enabled is True


@pytest.mark.parametrize("name", sorted(REQUIRED))
def test_missing_required_value_is_rejected(valid_env, name):
    valid_env.setenv(name, "")
    with pytest.raises(ValueError, match=name):
        load_config()


def test_weak_shared_secret_is_rejected(valid_env):
    valid_env.setenv("AAGASA_WORKER_SHARED_SECRET", "short")
    with pytest.raises(ValueError, match="AAGASA_WORKER_SHARED_SECRET"):
        load_config()


@pytest.mark.parametrize("value", ["abc", "0", "-1"])
def test_invalid_baud_rate_is_rejected(valid_env, value):
    valid_env.setenv("AAGASA_ROTATOR_BAUD_RATE", value)
    with pytest.raises(ValueError, match="AAGASA_ROTATOR_BAUD_RATE"):
        load_config()
