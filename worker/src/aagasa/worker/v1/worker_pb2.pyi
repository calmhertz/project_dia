from aagasa.worker.v1 import passplan_pb2 as _passplan_pb2
from aagasa.worker.v1 import recording_pb2 as _recording_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class SyncPassPlansRequest(_message.Message):
    __slots__ = ("worker_id", "station_id", "current_generation")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    STATION_ID_FIELD_NUMBER: _ClassVar[int]
    CURRENT_GENERATION_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    station_id: str
    current_generation: str
    def __init__(self, worker_id: _Optional[str] = ..., station_id: _Optional[str] = ..., current_generation: _Optional[str] = ...) -> None: ...

class SyncPassPlansResponse(_message.Message):
    __slots__ = ("changed", "plans")
    CHANGED_FIELD_NUMBER: _ClassVar[int]
    PLANS_FIELD_NUMBER: _ClassVar[int]
    changed: bool
    plans: _passplan_pb2.PassPlanSet
    def __init__(self, changed: _Optional[bool] = ..., plans: _Optional[_Union[_passplan_pb2.PassPlanSet, _Mapping]] = ...) -> None: ...

class RegisterRequest(_message.Message):
    __slots__ = ("worker_name", "worker_version", "available_pipelines")
    WORKER_NAME_FIELD_NUMBER: _ClassVar[int]
    WORKER_VERSION_FIELD_NUMBER: _ClassVar[int]
    AVAILABLE_PIPELINES_FIELD_NUMBER: _ClassVar[int]
    worker_name: str
    worker_version: str
    available_pipelines: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, worker_name: _Optional[str] = ..., worker_version: _Optional[str] = ..., available_pipelines: _Optional[_Iterable[str]] = ...) -> None: ...

class RegisterResponse(_message.Message):
    __slots__ = ("worker_id", "station_id", "station_name")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    STATION_ID_FIELD_NUMBER: _ClassVar[int]
    STATION_NAME_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    station_id: str
    station_name: str
    def __init__(self, worker_id: _Optional[str] = ..., station_id: _Optional[str] = ..., station_name: _Optional[str] = ...) -> None: ...

class HeartbeatRequest(_message.Message):
    __slots__ = ("worker_id", "worker_version", "state_generation", "pending_uploads", "pending_reports", "current_pass_id")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    WORKER_VERSION_FIELD_NUMBER: _ClassVar[int]
    STATE_GENERATION_FIELD_NUMBER: _ClassVar[int]
    PENDING_UPLOADS_FIELD_NUMBER: _ClassVar[int]
    PENDING_REPORTS_FIELD_NUMBER: _ClassVar[int]
    CURRENT_PASS_ID_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    worker_version: str
    state_generation: str
    pending_uploads: int
    pending_reports: int
    current_pass_id: str
    def __init__(self, worker_id: _Optional[str] = ..., worker_version: _Optional[str] = ..., state_generation: _Optional[str] = ..., pending_uploads: _Optional[int] = ..., pending_reports: _Optional[int] = ..., current_pass_id: _Optional[str] = ...) -> None: ...

class HeartbeatResponse(_message.Message):
    __slots__ = ("server_time_unix_ms", "desired_generation", "state_is_current")
    SERVER_TIME_UNIX_MS_FIELD_NUMBER: _ClassVar[int]
    DESIRED_GENERATION_FIELD_NUMBER: _ClassVar[int]
    STATE_IS_CURRENT_FIELD_NUMBER: _ClassVar[int]
    server_time_unix_ms: int
    desired_generation: str
    state_is_current: bool
    def __init__(self, server_time_unix_ms: _Optional[int] = ..., desired_generation: _Optional[str] = ..., state_is_current: _Optional[bool] = ...) -> None: ...

class PingRequest(_message.Message):
    __slots__ = ("worker_id", "station_id", "worker_version")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    STATION_ID_FIELD_NUMBER: _ClassVar[int]
    WORKER_VERSION_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    station_id: str
    worker_version: str
    def __init__(self, worker_id: _Optional[str] = ..., station_id: _Optional[str] = ..., worker_version: _Optional[str] = ...) -> None: ...

class PingResponse(_message.Message):
    __slots__ = ("server_time_unix_ms", "server_version")
    SERVER_TIME_UNIX_MS_FIELD_NUMBER: _ClassVar[int]
    SERVER_VERSION_FIELD_NUMBER: _ClassVar[int]
    server_time_unix_ms: int
    server_version: str
    def __init__(self, server_time_unix_ms: _Optional[int] = ..., server_version: _Optional[str] = ...) -> None: ...
