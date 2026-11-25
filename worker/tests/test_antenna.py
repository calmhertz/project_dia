import types

from aagasa_worker.antenna import AntennaModeStore
from aagasa_worker.hardware.rotator import G550Rotator
from aagasa_worker.hardware.station import Station
from aagasa_worker.main import Worker


class Port:
    def __init__(self, writes, fail=False):
        self.writes, self.fail = writes, fail

    def __call__(self, *args, **kwargs):
        if self.fail:
            raise OSError("no such port")
        return self

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def write(self, payload):
        self.writes.append(payload)


class Present:
    def check(self):
        return "stub"

    def pipelines(self):
        return []


def make_station(writes, fail=False):
    rotator = G550Rotator("/dev/ttyTEST", 9600, serial_factory=Port(writes, fail))
    return Station(rotator=rotator, sdr=Present(), satdump=Present())


def test_mode_defaults_to_a_rotator(tmp_path):
    assert AntennaModeStore(tmp_path).load() is False


def test_mode_survives_a_restart(tmp_path):
    AntennaModeStore(tmp_path).save(True)
    assert AntennaModeStore(tmp_path).load() is True
    AntennaModeStore(tmp_path).save(False)
    assert AntennaModeStore(tmp_path).load() is False


def test_a_corrupt_mode_file_falls_back_to_a_rotator(tmp_path):
    (tmp_path / "antenna_mode.json").write_text("{not json")
    assert AntennaModeStore(tmp_path).load() is False


def test_a_non_object_mode_file_falls_back_to_a_rotator(tmp_path):
    (tmp_path / "antenna_mode.json").write_text("[]")
    assert AntennaModeStore(tmp_path).load() is False


def test_a_fixed_antenna_is_not_probed_parked_or_reported(tmp_path):
    writes = []
    station = make_station(writes, fail=True)
    station.fixed_antenna = True

    status = station.safe_startup()
    station.safe_shutdown()

    assert status.rotator_enabled is False
    assert status.rotator_error == ""
    assert writes == []


def make_worker(station, store):
    worker = Worker.__new__(Worker)
    worker._station = station
    worker._antenna_store = store
    return worker


def test_the_server_setting_is_applied_and_remembered(tmp_path):
    writes = []
    station = make_station(writes)
    store = AntennaModeStore(tmp_path)
    worker = make_worker(station, store)

    worker._apply_antenna_mode(True)
    assert station.fixed_antenna is True
    assert store.load() is True
    assert writes == []

    worker._apply_antenna_mode(False)
    assert station.fixed_antenna is False
    assert store.load() is False
    assert writes == [b"W0000 000\r"], "re-enabling the rotator should park it"


def test_an_unchanged_setting_does_nothing(tmp_path):
    writes = []
    station = make_station(writes)
    worker = make_worker(station, AntennaModeStore(tmp_path))

    worker._apply_antenna_mode(False)

    assert writes == []
    assert not (tmp_path / "antenna_mode.json").exists()
