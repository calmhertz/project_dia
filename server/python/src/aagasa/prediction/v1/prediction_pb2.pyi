import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class HealthRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class HealthResponse(_message.Message):
    __slots__ = ("status", "version")
    class Status(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        STATUS_UNSPECIFIED: _ClassVar[HealthResponse.Status]
        STATUS_SERVING: _ClassVar[HealthResponse.Status]
        STATUS_NOT_SERVING: _ClassVar[HealthResponse.Status]
    STATUS_UNSPECIFIED: HealthResponse.Status
    STATUS_SERVING: HealthResponse.Status
    STATUS_NOT_SERVING: HealthResponse.Status
    STATUS_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    status: HealthResponse.Status
    version: str
    def __init__(self, status: _Optional[_Union[HealthResponse.Status, str]] = ..., version: _Optional[str] = ...) -> None: ...

class TwoLineElements(_message.Message):
    __slots__ = ("line1", "line2")
    LINE1_FIELD_NUMBER: _ClassVar[int]
    LINE2_FIELD_NUMBER: _ClassVar[int]
    line1: str
    line2: str
    def __init__(self, line1: _Optional[str] = ..., line2: _Optional[str] = ...) -> None: ...

class ObserverLocation(_message.Message):
    __slots__ = ("latitude_degrees", "longitude_degrees", "altitude_m")
    LATITUDE_DEGREES_FIELD_NUMBER: _ClassVar[int]
    LONGITUDE_DEGREES_FIELD_NUMBER: _ClassVar[int]
    ALTITUDE_M_FIELD_NUMBER: _ClassVar[int]
    latitude_degrees: float
    longitude_degrees: float
    altitude_m: float
    def __init__(self, latitude_degrees: _Optional[float] = ..., longitude_degrees: _Optional[float] = ..., altitude_m: _Optional[float] = ...) -> None: ...

class PredictPassesRequest(_message.Message):
    __slots__ = ("tle", "observer", "minimum_elevation_degrees", "search_start", "search_end", "track_step_seconds", "max_passes")
    TLE_FIELD_NUMBER: _ClassVar[int]
    OBSERVER_FIELD_NUMBER: _ClassVar[int]
    MINIMUM_ELEVATION_DEGREES_FIELD_NUMBER: _ClassVar[int]
    SEARCH_START_FIELD_NUMBER: _ClassVar[int]
    SEARCH_END_FIELD_NUMBER: _ClassVar[int]
    TRACK_STEP_SECONDS_FIELD_NUMBER: _ClassVar[int]
    MAX_PASSES_FIELD_NUMBER: _ClassVar[int]
    tle: TwoLineElements
    observer: ObserverLocation
    minimum_elevation_degrees: float
    search_start: _timestamp_pb2.Timestamp
    search_end: _timestamp_pb2.Timestamp
    track_step_seconds: int
    max_passes: int
    def __init__(self, tle: _Optional[_Union[TwoLineElements, _Mapping]] = ..., observer: _Optional[_Union[ObserverLocation, _Mapping]] = ..., minimum_elevation_degrees: _Optional[float] = ..., search_start: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., search_end: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., track_step_seconds: _Optional[int] = ..., max_passes: _Optional[int] = ...) -> None: ...

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

class PredictedPass(_message.Message):
    __slots__ = ("aos", "tca", "los", "aos_azimuth_degrees", "tca_azimuth_degrees", "los_azimuth_degrees", "max_elevation_degrees", "duration_seconds", "track")
    AOS_FIELD_NUMBER: _ClassVar[int]
    TCA_FIELD_NUMBER: _ClassVar[int]
    LOS_FIELD_NUMBER: _ClassVar[int]
    AOS_AZIMUTH_DEGREES_FIELD_NUMBER: _ClassVar[int]
    TCA_AZIMUTH_DEGREES_FIELD_NUMBER: _ClassVar[int]
    LOS_AZIMUTH_DEGREES_FIELD_NUMBER: _ClassVar[int]
    MAX_ELEVATION_DEGREES_FIELD_NUMBER: _ClassVar[int]
    DURATION_SECONDS_FIELD_NUMBER: _ClassVar[int]
    TRACK_FIELD_NUMBER: _ClassVar[int]
    aos: _timestamp_pb2.Timestamp
    tca: _timestamp_pb2.Timestamp
    los: _timestamp_pb2.Timestamp
    aos_azimuth_degrees: float
    tca_azimuth_degrees: float
    los_azimuth_degrees: float
    max_elevation_degrees: float
    duration_seconds: float
    track: _containers.RepeatedCompositeFieldContainer[TrackPoint]
    def __init__(self, aos: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., tca: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., los: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., aos_azimuth_degrees: _Optional[float] = ..., tca_azimuth_degrees: _Optional[float] = ..., los_azimuth_degrees: _Optional[float] = ..., max_elevation_degrees: _Optional[float] = ..., duration_seconds: _Optional[float] = ..., track: _Optional[_Iterable[_Union[TrackPoint, _Mapping]]] = ...) -> None: ...

class PredictPassesResponse(_message.Message):
    __slots__ = ("norad_id", "passes")
    NORAD_ID_FIELD_NUMBER: _ClassVar[int]
    PASSES_FIELD_NUMBER: _ClassVar[int]
    norad_id: int
    passes: _containers.RepeatedCompositeFieldContainer[PredictedPass]
    def __init__(self, norad_id: _Optional[int] = ..., passes: _Optional[_Iterable[_Union[PredictedPass, _Mapping]]] = ...) -> None: ...
