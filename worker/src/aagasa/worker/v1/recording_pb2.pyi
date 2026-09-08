import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class RecordingMetadata(_message.Message):
    __slots__ = ("recording_id", "pass_id", "worker_id", "station_id", "relative_path", "size_bytes", "checksum_sha256", "started_at", "finished_at")
    RECORDING_ID_FIELD_NUMBER: _ClassVar[int]
    PASS_ID_FIELD_NUMBER: _ClassVar[int]
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    STATION_ID_FIELD_NUMBER: _ClassVar[int]
    RELATIVE_PATH_FIELD_NUMBER: _ClassVar[int]
    SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_SHA256_FIELD_NUMBER: _ClassVar[int]
    STARTED_AT_FIELD_NUMBER: _ClassVar[int]
    FINISHED_AT_FIELD_NUMBER: _ClassVar[int]
    recording_id: str
    pass_id: str
    worker_id: str
    station_id: str
    relative_path: str
    size_bytes: int
    checksum_sha256: str
    started_at: _timestamp_pb2.Timestamp
    finished_at: _timestamp_pb2.Timestamp
    def __init__(self, recording_id: _Optional[str] = ..., pass_id: _Optional[str] = ..., worker_id: _Optional[str] = ..., station_id: _Optional[str] = ..., relative_path: _Optional[str] = ..., size_bytes: _Optional[int] = ..., checksum_sha256: _Optional[str] = ..., started_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., finished_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class UploadRecordingRequest(_message.Message):
    __slots__ = ("metadata", "chunk")
    METADATA_FIELD_NUMBER: _ClassVar[int]
    CHUNK_FIELD_NUMBER: _ClassVar[int]
    metadata: RecordingMetadata
    chunk: bytes
    def __init__(self, metadata: _Optional[_Union[RecordingMetadata, _Mapping]] = ..., chunk: _Optional[bytes] = ...) -> None: ...

class UploadRecordingResponse(_message.Message):
    __slots__ = ("recording_id", "stored", "size_bytes")
    RECORDING_ID_FIELD_NUMBER: _ClassVar[int]
    STORED_FIELD_NUMBER: _ClassVar[int]
    SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    recording_id: str
    stored: bool
    size_bytes: int
    def __init__(self, recording_id: _Optional[str] = ..., stored: _Optional[bool] = ..., size_bytes: _Optional[int] = ...) -> None: ...

class ExecutionRecord(_message.Message):
    __slots__ = ("pass_id", "state", "detail", "recorded_at")
    PASS_ID_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    DETAIL_FIELD_NUMBER: _ClassVar[int]
    RECORDED_AT_FIELD_NUMBER: _ClassVar[int]
    pass_id: str
    state: str
    detail: str
    recorded_at: _timestamp_pb2.Timestamp
    def __init__(self, pass_id: _Optional[str] = ..., state: _Optional[str] = ..., detail: _Optional[str] = ..., recorded_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class ReportExecutionsRequest(_message.Message):
    __slots__ = ("worker_id", "records")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    RECORDS_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    records: _containers.RepeatedCompositeFieldContainer[ExecutionRecord]
    def __init__(self, worker_id: _Optional[str] = ..., records: _Optional[_Iterable[_Union[ExecutionRecord, _Mapping]]] = ...) -> None: ...

class ReportExecutionsResponse(_message.Message):
    __slots__ = ("accepted",)
    ACCEPTED_FIELD_NUMBER: _ClassVar[int]
    accepted: int
    def __init__(self, accepted: _Optional[int] = ...) -> None: ...
