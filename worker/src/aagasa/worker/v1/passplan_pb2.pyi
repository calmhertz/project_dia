import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class RFBand(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    RF_BAND_UNSPECIFIED: _ClassVar[RFBand]
    RF_BAND_VHF: _ClassVar[RFBand]
    RF_BAND_UHF: _ClassVar[RFBand]

class RecordingMode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    RECORDING_MODE_UNSPECIFIED: _ClassVar[RecordingMode]
    RECORDING_MODE_RAW: _ClassVar[RecordingMode]
    RECORDING_MODE_PROCESS: _ClassVar[RecordingMode]
    RECORDING_MODE_RAW_AND_PROCESS: _ClassVar[RecordingMode]
RF_BAND_UNSPECIFIED: RFBand
RF_BAND_VHF: RFBand
RF_BAND_UHF: RFBand
RECORDING_MODE_UNSPECIFIED: RecordingMode
RECORDING_MODE_RAW: RecordingMode
RECORDING_MODE_PROCESS: RecordingMode
RECORDING_MODE_RAW_AND_PROCESS: RecordingMode

class OrbitalElements(_message.Message):
    __slots__ = ("line1", "line2", "epoch", "tle_record_id")
    LINE1_FIELD_NUMBER: _ClassVar[int]
    LINE2_FIELD_NUMBER: _ClassVar[int]
    EPOCH_FIELD_NUMBER: _ClassVar[int]
    TLE_RECORD_ID_FIELD_NUMBER: _ClassVar[int]
    line1: str
    line2: str
    epoch: _timestamp_pb2.Timestamp
    tle_record_id: str
    def __init__(self, line1: _Optional[str] = ..., line2: _Optional[str] = ..., epoch: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., tle_record_id: _Optional[str] = ...) -> None: ...

class RadioSettings(_message.Message):
    __slots__ = ("source", "frequency_hz", "sample_rate_hz", "gain_db", "ppm_correction", "bias_tee_enabled")
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    FREQUENCY_HZ_FIELD_NUMBER: _ClassVar[int]
    SAMPLE_RATE_HZ_FIELD_NUMBER: _ClassVar[int]
    GAIN_DB_FIELD_NUMBER: _ClassVar[int]
    PPM_CORRECTION_FIELD_NUMBER: _ClassVar[int]
    BIAS_TEE_ENABLED_FIELD_NUMBER: _ClassVar[int]
    source: str
    frequency_hz: int
    sample_rate_hz: int
    gain_db: float
    ppm_correction: int
    bias_tee_enabled: bool
    def __init__(self, source: _Optional[str] = ..., frequency_hz: _Optional[int] = ..., sample_rate_hz: _Optional[int] = ..., gain_db: _Optional[float] = ..., ppm_correction: _Optional[int] = ..., bias_tee_enabled: _Optional[bool] = ...) -> None: ...

class SatDumpPipeline(_message.Message):
    __slots__ = ("identifier", "is_custom", "definition_json", "checksum_sha256")
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    IS_CUSTOM_FIELD_NUMBER: _ClassVar[int]
    DEFINITION_JSON_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_SHA256_FIELD_NUMBER: _ClassVar[int]
    identifier: str
    is_custom: bool
    definition_json: str
    checksum_sha256: str
    def __init__(self, identifier: _Optional[str] = ..., is_custom: _Optional[bool] = ..., definition_json: _Optional[str] = ..., checksum_sha256: _Optional[str] = ...) -> None: ...

class TrackPoint(_message.Message):
    __slots__ = ("at", "azimuth_degrees", "elevation_degrees", "range_km")
    AT_FIELD_NUMBER: _ClassVar[int]
    AZIMUTH_DEGREES_FIELD_NUMBER: _ClassVar[int]
    ELEVATION_DEGREES_FIELD_NUMBER: _ClassVar[int]
    RANGE_KM_FIELD_NUMBER: _ClassVar[int]
    at: _timestamp_pb2.Timestamp
    azimuth_degrees: float
    elevation_degrees: float
    range_km: float
    def __init__(self, at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., azimuth_degrees: _Optional[float] = ..., elevation_degrees: _Optional[float] = ..., range_km: _Optional[float] = ...) -> None: ...

class PassPlan(_message.Message):
    __slots__ = ("plan_version", "pass_id", "station_id", "worker_id", "norad_id", "satellite_name", "elements", "aos", "tca", "los", "max_elevation_degrees", "recording_start", "recording_end", "band", "radio", "recording_mode", "pipeline", "track", "generation", "generated_at")
    PLAN_VERSION_FIELD_NUMBER: _ClassVar[int]
    PASS_ID_FIELD_NUMBER: _ClassVar[int]
    STATION_ID_FIELD_NUMBER: _ClassVar[int]
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    NORAD_ID_FIELD_NUMBER: _ClassVar[int]
    SATELLITE_NAME_FIELD_NUMBER: _ClassVar[int]
    ELEMENTS_FIELD_NUMBER: _ClassVar[int]
    AOS_FIELD_NUMBER: _ClassVar[int]
    TCA_FIELD_NUMBER: _ClassVar[int]
    LOS_FIELD_NUMBER: _ClassVar[int]
    MAX_ELEVATION_DEGREES_FIELD_NUMBER: _ClassVar[int]
    RECORDING_START_FIELD_NUMBER: _ClassVar[int]
    RECORDING_END_FIELD_NUMBER: _ClassVar[int]
    BAND_FIELD_NUMBER: _ClassVar[int]
    RADIO_FIELD_NUMBER: _ClassVar[int]
    RECORDING_MODE_FIELD_NUMBER: _ClassVar[int]
    PIPELINE_FIELD_NUMBER: _ClassVar[int]
    TRACK_FIELD_NUMBER: _ClassVar[int]
    GENERATION_FIELD_NUMBER: _ClassVar[int]
    GENERATED_AT_FIELD_NUMBER: _ClassVar[int]
    plan_version: int
    pass_id: str
    station_id: str
    worker_id: str
    norad_id: int
    satellite_name: str
    elements: OrbitalElements
    aos: _timestamp_pb2.Timestamp
    tca: _timestamp_pb2.Timestamp
    los: _timestamp_pb2.Timestamp
    max_elevation_degrees: float
    recording_start: _timestamp_pb2.Timestamp
    recording_end: _timestamp_pb2.Timestamp
    band: RFBand
    radio: RadioSettings
    recording_mode: RecordingMode
    pipeline: SatDumpPipeline
    track: _containers.RepeatedCompositeFieldContainer[TrackPoint]
    generation: str
    generated_at: _timestamp_pb2.Timestamp
    def __init__(self, plan_version: _Optional[int] = ..., pass_id: _Optional[str] = ..., station_id: _Optional[str] = ..., worker_id: _Optional[str] = ..., norad_id: _Optional[int] = ..., satellite_name: _Optional[str] = ..., elements: _Optional[_Union[OrbitalElements, _Mapping]] = ..., aos: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., tca: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., los: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., max_elevation_degrees: _Optional[float] = ..., recording_start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., recording_end: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., band: _Optional[_Union[RFBand, str]] = ..., radio: _Optional[_Union[RadioSettings, _Mapping]] = ..., recording_mode: _Optional[_Union[RecordingMode, str]] = ..., pipeline: _Optional[_Union[SatDumpPipeline, _Mapping]] = ..., track: _Optional[_Iterable[_Union[TrackPoint, _Mapping]]] = ..., generation: _Optional[str] = ..., generated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class PassPlanSet(_message.Message):
    __slots__ = ("plans", "generation", "generated_at")
    PLANS_FIELD_NUMBER: _ClassVar[int]
    GENERATION_FIELD_NUMBER: _ClassVar[int]
    GENERATED_AT_FIELD_NUMBER: _ClassVar[int]
    plans: _containers.RepeatedCompositeFieldContainer[PassPlan]
    generation: str
    generated_at: _timestamp_pb2.Timestamp
    def __init__(self, plans: _Optional[_Iterable[_Union[PassPlan, _Mapping]]] = ..., generation: _Optional[str] = ..., generated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...
