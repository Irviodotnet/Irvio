// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me
// This is a generated file - do not edit.
//
// Generated from hydra/local/v1/control.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports
// ignore_for_file: unused_import

import 'dart:convert' as $convert;
import 'dart:core' as $core;
import 'dart:typed_data' as $typed_data;

@$core.Deprecated('Use methodDescriptor instead')
const Method$json = {
  '1': 'Method',
  '2': [
    {'1': 'METHOD_UNSPECIFIED', '2': 0},
    {'1': 'METHOD_HELLO', '2': 1},
    {'1': 'METHOD_GET_STATUS', '2': 2},
    {'1': 'METHOD_CONNECT', '2': 3},
    {'1': 'METHOD_DISCONNECT', '2': 4},
    {'1': 'METHOD_GET_CONTRIBUTION', '2': 5},
    {'1': 'METHOD_SET_CONTRIBUTION', '2': 6},
    {'1': 'METHOD_SUBSCRIBE_EVENTS', '2': 7},
    {'1': 'METHOD_GET_RECENT_LOGS', '2': 8},
    {'1': 'METHOD_SUBSCRIBE_LOGS', '2': 9},
    {'1': 'METHOD_GET_BOOTSTRAP_STATE', '2': 10},
    {'1': 'METHOD_IMPORT_BOOTSTRAP_BUNDLE', '2': 11},
    {'1': 'METHOD_GENERATE_BOOTSTRAP_BUNDLE', '2': 12},
    {'1': 'METHOD_DELETE_PROFILE', '2': 13},
    {'1': 'METHOD_GET_AVAILABLE_COUNTRIES', '2': 14},
    {'1': 'METHOD_SHUTDOWN', '2': 15},
    {'1': 'METHOD_GET_TRAFFIC_STATS', '2': 16},
  ],
};

/// Descriptor for `Method`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List methodDescriptor = $convert.base64Decode(
    'CgZNZXRob2QSFgoSTUVUSE9EX1VOU1BFQ0lGSUVEEAASEAoMTUVUSE9EX0hFTExPEAESFQoRTU'
    'VUSE9EX0dFVF9TVEFUVVMQAhISCg5NRVRIT0RfQ09OTkVDVBADEhUKEU1FVEhPRF9ESVNDT05O'
    'RUNUEAQSGwoXTUVUSE9EX0dFVF9DT05UUklCVVRJT04QBRIbChdNRVRIT0RfU0VUX0NPTlRSSU'
    'JVVElPThAGEhsKF01FVEhPRF9TVUJTQ1JJQkVfRVZFTlRTEAcSGgoWTUVUSE9EX0dFVF9SRUNF'
    'TlRfTE9HUxAIEhkKFU1FVEhPRF9TVUJTQ1JJQkVfTE9HUxAJEh4KGk1FVEhPRF9HRVRfQk9PVF'
    'NUUkFQX1NUQVRFEAoSIgoeTUVUSE9EX0lNUE9SVF9CT09UU1RSQVBfQlVORExFEAsSJAogTUVU'
    'SE9EX0dFTkVSQVRFX0JPT1RTVFJBUF9CVU5ETEUQDBIZChVNRVRIT0RfREVMRVRFX1BST0ZJTE'
    'UQDRIiCh5NRVRIT0RfR0VUX0FWQUlMQUJMRV9DT1VOVFJJRVMQDhITCg9NRVRIT0RfU0hVVERP'
    'V04QDxIcChhNRVRIT0RfR0VUX1RSQUZGSUNfU1RBVFMQEA==');

@$core.Deprecated('Use resultCodeDescriptor instead')
const ResultCode$json = {
  '1': 'ResultCode',
  '2': [
    {'1': 'RESULT_CODE_UNSPECIFIED', '2': 0},
    {'1': 'RESULT_CODE_OK', '2': 1},
    {'1': 'RESULT_CODE_INVALID_ARGUMENT', '2': 2},
    {'1': 'RESULT_CODE_UNSUPPORTED_API', '2': 3},
    {'1': 'RESULT_CODE_UNAUTHORIZED', '2': 4},
    {'1': 'RESULT_CODE_BUSY', '2': 5},
    {'1': 'RESULT_CODE_NOT_READY', '2': 6},
    {'1': 'RESULT_CODE_UNSUPPORTED', '2': 7},
    {'1': 'RESULT_CODE_INTERNAL', '2': 8},
  ],
};

/// Descriptor for `ResultCode`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List resultCodeDescriptor = $convert.base64Decode(
    'CgpSZXN1bHRDb2RlEhsKF1JFU1VMVF9DT0RFX1VOU1BFQ0lGSUVEEAASEgoOUkVTVUxUX0NPRE'
    'VfT0sQARIgChxSRVNVTFRfQ09ERV9JTlZBTElEX0FSR1VNRU5UEAISHwobUkVTVUxUX0NPREVf'
    'VU5TVVBQT1JURURfQVBJEAMSHAoYUkVTVUxUX0NPREVfVU5BVVRIT1JJWkVEEAQSFAoQUkVTVU'
    'xUX0NPREVfQlVTWRAFEhkKFVJFU1VMVF9DT0RFX05PVF9SRUFEWRAGEhsKF1JFU1VMVF9DT0RF'
    'X1VOU1VQUE9SVEVEEAcSGAoUUkVTVUxUX0NPREVfSU5URVJOQUwQCA==');

@$core.Deprecated('Use connectionStateDescriptor instead')
const ConnectionState$json = {
  '1': 'ConnectionState',
  '2': [
    {'1': 'CONNECTION_STATE_UNSPECIFIED', '2': 0},
    {'1': 'CONNECTION_STATE_DISCONNECTED', '2': 1},
    {'1': 'CONNECTION_STATE_CONNECTING', '2': 2},
    {'1': 'CONNECTION_STATE_CONNECTED', '2': 3},
    {'1': 'CONNECTION_STATE_RECOVERING', '2': 4},
    {'1': 'CONNECTION_STATE_NO_NETWORK', '2': 5},
    {'1': 'CONNECTION_STATE_NO_EXIT', '2': 6},
    {'1': 'CONNECTION_STATE_DISCONNECTING', '2': 7},
    {'1': 'CONNECTION_STATE_ERROR', '2': 8},
  ],
};

/// Descriptor for `ConnectionState`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List connectionStateDescriptor = $convert.base64Decode(
    'Cg9Db25uZWN0aW9uU3RhdGUSIAocQ09OTkVDVElPTl9TVEFURV9VTlNQRUNJRklFRBAAEiEKHU'
    'NPTk5FQ1RJT05fU1RBVEVfRElTQ09OTkVDVEVEEAESHwobQ09OTkVDVElPTl9TVEFURV9DT05O'
    'RUNUSU5HEAISHgoaQ09OTkVDVElPTl9TVEFURV9DT05ORUNURUQQAxIfChtDT05ORUNUSU9OX1'
    'NUQVRFX1JFQ09WRVJJTkcQBBIfChtDT05ORUNUSU9OX1NUQVRFX05PX05FVFdPUksQBRIcChhD'
    'T05ORUNUSU9OX1NUQVRFX05PX0VYSVQQBhIiCh5DT05ORUNUSU9OX1NUQVRFX0RJU0NPTk5FQ1'
    'RJTkcQBxIaChZDT05ORUNUSU9OX1NUQVRFX0VSUk9SEAg=');

@$core.Deprecated('Use logSeverityDescriptor instead')
const LogSeverity$json = {
  '1': 'LogSeverity',
  '2': [
    {'1': 'LOG_SEVERITY_UNSPECIFIED', '2': 0},
    {'1': 'LOG_SEVERITY_INFO', '2': 1},
    {'1': 'LOG_SEVERITY_WARNING', '2': 2},
    {'1': 'LOG_SEVERITY_ERROR', '2': 3},
  ],
};

/// Descriptor for `LogSeverity`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List logSeverityDescriptor = $convert.base64Decode(
    'CgtMb2dTZXZlcml0eRIcChhMT0dfU0VWRVJJVFlfVU5TUEVDSUZJRUQQABIVChFMT0dfU0VWRV'
    'JJVFlfSU5GTxABEhgKFExPR19TRVZFUklUWV9XQVJOSU5HEAISFgoSTE9HX1NFVkVSSVRZX0VS'
    'Uk9SEAM=');

@$core.Deprecated('Use bootstrapPhaseDescriptor instead')
const BootstrapPhase$json = {
  '1': 'BootstrapPhase',
  '2': [
    {'1': 'BOOTSTRAP_PHASE_UNSPECIFIED', '2': 0},
    {'1': 'BOOTSTRAP_PHASE_EMPTY', '2': 1},
    {'1': 'BOOTSTRAP_PHASE_CONNECTING', '2': 2},
    {'1': 'BOOTSTRAP_PHASE_READY', '2': 3},
    {'1': 'BOOTSTRAP_PHASE_DEGRADED', '2': 4},
    {'1': 'BOOTSTRAP_PHASE_FAILED', '2': 5},
  ],
};

/// Descriptor for `BootstrapPhase`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List bootstrapPhaseDescriptor = $convert.base64Decode(
    'Cg5Cb290c3RyYXBQaGFzZRIfChtCT09UU1RSQVBfUEhBU0VfVU5TUEVDSUZJRUQQABIZChVCT0'
    '9UU1RSQVBfUEhBU0VfRU1QVFkQARIeChpCT09UU1RSQVBfUEhBU0VfQ09OTkVDVElORxACEhkK'
    'FUJPT1RTVFJBUF9QSEFTRV9SRUFEWRADEhwKGEJPT1RTVFJBUF9QSEFTRV9ERUdSQURFRBAEEh'
    'oKFkJPT1RTVFJBUF9QSEFTRV9GQUlMRUQQBQ==');

@$core.Deprecated('Use availableCountriesStatusDescriptor instead')
const AvailableCountriesStatus$json = {
  '1': 'AvailableCountriesStatus',
  '2': [
    {'1': 'AVAILABLE_COUNTRIES_STATUS_UNSPECIFIED', '2': 0},
    {'1': 'AVAILABLE_COUNTRIES_STATUS_READY', '2': 1},
    {'1': 'AVAILABLE_COUNTRIES_STATUS_PARTIAL', '2': 2},
    {'1': 'AVAILABLE_COUNTRIES_STATUS_EMPTY', '2': 3},
    {'1': 'AVAILABLE_COUNTRIES_STATUS_UNAVAILABLE', '2': 4},
  ],
};

/// Descriptor for `AvailableCountriesStatus`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List availableCountriesStatusDescriptor = $convert.base64Decode(
    'ChhBdmFpbGFibGVDb3VudHJpZXNTdGF0dXMSKgomQVZBSUxBQkxFX0NPVU5UUklFU19TVEFUVV'
    'NfVU5TUEVDSUZJRUQQABIkCiBBVkFJTEFCTEVfQ09VTlRSSUVTX1NUQVRVU19SRUFEWRABEiYK'
    'IkFWQUlMQUJMRV9DT1VOVFJJRVNfU1RBVFVTX1BBUlRJQUwQAhIkCiBBVkFJTEFCTEVfQ09VTl'
    'RSSUVTX1NUQVRVU19FTVBUWRADEioKJkFWQUlMQUJMRV9DT1VOVFJJRVNfU1RBVFVTX1VOQVZB'
    'SUxBQkxFEAQ=');

@$core.Deprecated('Use envelopeDescriptor instead')
const Envelope$json = {
  '1': 'Envelope',
  '2': [
    {'1': 'api_major', '3': 1, '4': 1, '5': 13, '10': 'apiMajor'},
    {'1': 'api_minor', '3': 2, '4': 1, '5': 13, '10': 'apiMinor'},
    {'1': 'request_id', '3': 3, '4': 1, '5': 4, '10': 'requestId'},
    {
      '1': 'request',
      '3': 10,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.Request',
      '9': 0,
      '10': 'request'
    },
    {
      '1': 'response',
      '3': 11,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.Response',
      '9': 0,
      '10': 'response'
    },
    {
      '1': 'event',
      '3': 12,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.Event',
      '9': 0,
      '10': 'event'
    },
  ],
  '8': [
    {'1': 'payload'},
  ],
};

/// Descriptor for `Envelope`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List envelopeDescriptor = $convert.base64Decode(
    'CghFbnZlbG9wZRIbCglhcGlfbWFqb3IYASABKA1SCGFwaU1ham9yEhsKCWFwaV9taW5vchgCIA'
    'EoDVIIYXBpTWlub3ISHQoKcmVxdWVzdF9pZBgDIAEoBFIJcmVxdWVzdElkEjMKB3JlcXVlc3QY'
    'CiABKAsyFy5oeWRyYS5sb2NhbC52MS5SZXF1ZXN0SABSB3JlcXVlc3QSNgoIcmVzcG9uc2UYCy'
    'ABKAsyGC5oeWRyYS5sb2NhbC52MS5SZXNwb25zZUgAUghyZXNwb25zZRItCgVldmVudBgMIAEo'
    'CzIVLmh5ZHJhLmxvY2FsLnYxLkV2ZW50SABSBWV2ZW50QgkKB3BheWxvYWQ=');

@$core.Deprecated('Use requestDescriptor instead')
const Request$json = {
  '1': 'Request',
  '2': [
    {
      '1': 'method',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.hydra.local.v1.Method',
      '10': 'method'
    },
    {'1': 'country_code', '3': 2, '4': 1, '5': 9, '10': 'countryCode'},
    {
      '1': 'contribution_enabled',
      '3': 3,
      '4': 1,
      '5': 8,
      '10': 'contributionEnabled'
    },
    {
      '1': 'get_recent_logs',
      '3': 10,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.GetRecentLogsRequest',
      '10': 'getRecentLogs'
    },
    {
      '1': 'subscribe_logs',
      '3': 11,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.SubscribeLogsRequest',
      '10': 'subscribeLogs'
    },
    {
      '1': 'import_bootstrap_bundle',
      '3': 12,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.ImportBootstrapBundleRequest',
      '10': 'importBootstrapBundle'
    },
    {
      '1': 'generate_bootstrap_bundle',
      '3': 13,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.GenerateBootstrapBundleRequest',
      '10': 'generateBootstrapBundle'
    },
    {
      '1': 'delete_profile',
      '3': 14,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.DeleteProfileRequest',
      '10': 'deleteProfile'
    },
    {
      '1': 'get_available_countries',
      '3': 15,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.GetAvailableCountriesRequest',
      '10': 'getAvailableCountries'
    },
  ],
};

/// Descriptor for `Request`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List requestDescriptor = $convert.base64Decode(
    'CgdSZXF1ZXN0Ei4KBm1ldGhvZBgBIAEoDjIWLmh5ZHJhLmxvY2FsLnYxLk1ldGhvZFIGbWV0aG'
    '9kEiEKDGNvdW50cnlfY29kZRgCIAEoCVILY291bnRyeUNvZGUSMQoUY29udHJpYnV0aW9uX2Vu'
    'YWJsZWQYAyABKAhSE2NvbnRyaWJ1dGlvbkVuYWJsZWQSTAoPZ2V0X3JlY2VudF9sb2dzGAogAS'
    'gLMiQuaHlkcmEubG9jYWwudjEuR2V0UmVjZW50TG9nc1JlcXVlc3RSDWdldFJlY2VudExvZ3MS'
    'SwoOc3Vic2NyaWJlX2xvZ3MYCyABKAsyJC5oeWRyYS5sb2NhbC52MS5TdWJzY3JpYmVMb2dzUm'
    'VxdWVzdFINc3Vic2NyaWJlTG9ncxJkChdpbXBvcnRfYm9vdHN0cmFwX2J1bmRsZRgMIAEoCzIs'
    'Lmh5ZHJhLmxvY2FsLnYxLkltcG9ydEJvb3RzdHJhcEJ1bmRsZVJlcXVlc3RSFWltcG9ydEJvb3'
    'RzdHJhcEJ1bmRsZRJqChlnZW5lcmF0ZV9ib290c3RyYXBfYnVuZGxlGA0gASgLMi4uaHlkcmEu'
    'bG9jYWwudjEuR2VuZXJhdGVCb290c3RyYXBCdW5kbGVSZXF1ZXN0UhdnZW5lcmF0ZUJvb3RzdH'
    'JhcEJ1bmRsZRJLCg5kZWxldGVfcHJvZmlsZRgOIAEoCzIkLmh5ZHJhLmxvY2FsLnYxLkRlbGV0'
    'ZVByb2ZpbGVSZXF1ZXN0Ug1kZWxldGVQcm9maWxlEmQKF2dldF9hdmFpbGFibGVfY291bnRyaW'
    'VzGA8gASgLMiwuaHlkcmEubG9jYWwudjEuR2V0QXZhaWxhYmxlQ291bnRyaWVzUmVxdWVzdFIV'
    'Z2V0QXZhaWxhYmxlQ291bnRyaWVz');

@$core.Deprecated('Use helloDescriptor instead')
const Hello$json = {
  '1': 'Hello',
  '2': [
    {'1': 'server_api_major', '3': 1, '4': 1, '5': 13, '10': 'serverApiMajor'},
    {'1': 'server_api_minor', '3': 2, '4': 1, '5': 13, '10': 'serverApiMinor'},
    {
      '1': 'negotiated_api_minor',
      '3': 3,
      '4': 1,
      '5': 13,
      '10': 'negotiatedApiMinor'
    },
    {'1': 'product_version', '3': 4, '4': 1, '5': 9, '10': 'productVersion'},
    {'1': 'core_version', '3': 5, '4': 1, '5': 9, '10': 'coreVersion'},
  ],
};

/// Descriptor for `Hello`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List helloDescriptor = $convert.base64Decode(
    'CgVIZWxsbxIoChBzZXJ2ZXJfYXBpX21ham9yGAEgASgNUg5zZXJ2ZXJBcGlNYWpvchIoChBzZX'
    'J2ZXJfYXBpX21pbm9yGAIgASgNUg5zZXJ2ZXJBcGlNaW5vchIwChRuZWdvdGlhdGVkX2FwaV9t'
    'aW5vchgDIAEoDVISbmVnb3RpYXRlZEFwaU1pbm9yEicKD3Byb2R1Y3RfdmVyc2lvbhgEIAEoCV'
    'IOcHJvZHVjdFZlcnNpb24SIQoMY29yZV92ZXJzaW9uGAUgASgJUgtjb3JlVmVyc2lvbg==');

@$core.Deprecated('Use responseDescriptor instead')
const Response$json = {
  '1': 'Response',
  '2': [
    {
      '1': 'code',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.hydra.local.v1.ResultCode',
      '10': 'code'
    },
    {'1': 'user_message', '3': 2, '4': 1, '5': 9, '10': 'userMessage'},
    {'1': 'accepted', '3': 3, '4': 1, '5': 8, '10': 'accepted'},
    {
      '1': 'hello',
      '3': 10,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.Hello',
      '9': 0,
      '10': 'hello'
    },
    {
      '1': 'status',
      '3': 11,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.Status',
      '9': 0,
      '10': 'status'
    },
    {
      '1': 'contribution',
      '3': 12,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.Contribution',
      '9': 0,
      '10': 'contribution'
    },
    {
      '1': 'log_tail',
      '3': 13,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.LogTail',
      '9': 0,
      '10': 'logTail'
    },
    {
      '1': 'bootstrap_state',
      '3': 14,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.BootstrapState',
      '9': 0,
      '10': 'bootstrapState'
    },
    {
      '1': 'generated_bootstrap_bundle',
      '3': 15,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.GeneratedBootstrapBundle',
      '9': 0,
      '10': 'generatedBootstrapBundle'
    },
    {
      '1': 'available_countries',
      '3': 16,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.AvailableCountries',
      '9': 0,
      '10': 'availableCountries'
    },
    {
      '1': 'traffic_stats',
      '3': 17,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.TrafficStats',
      '9': 0,
      '10': 'trafficStats'
    },
  ],
  '8': [
    {'1': 'payload'},
  ],
};

/// Descriptor for `Response`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List responseDescriptor = $convert.base64Decode(
    'CghSZXNwb25zZRIuCgRjb2RlGAEgASgOMhouaHlkcmEubG9jYWwudjEuUmVzdWx0Q29kZVIEY2'
    '9kZRIhCgx1c2VyX21lc3NhZ2UYAiABKAlSC3VzZXJNZXNzYWdlEhoKCGFjY2VwdGVkGAMgASgI'
    'UghhY2NlcHRlZBItCgVoZWxsbxgKIAEoCzIVLmh5ZHJhLmxvY2FsLnYxLkhlbGxvSABSBWhlbG'
    'xvEjAKBnN0YXR1cxgLIAEoCzIWLmh5ZHJhLmxvY2FsLnYxLlN0YXR1c0gAUgZzdGF0dXMSQgoM'
    'Y29udHJpYnV0aW9uGAwgASgLMhwuaHlkcmEubG9jYWwudjEuQ29udHJpYnV0aW9uSABSDGNvbn'
    'RyaWJ1dGlvbhI0Cghsb2dfdGFpbBgNIAEoCzIXLmh5ZHJhLmxvY2FsLnYxLkxvZ1RhaWxIAFIH'
    'bG9nVGFpbBJJCg9ib290c3RyYXBfc3RhdGUYDiABKAsyHi5oeWRyYS5sb2NhbC52MS5Cb290c3'
    'RyYXBTdGF0ZUgAUg5ib290c3RyYXBTdGF0ZRJoChpnZW5lcmF0ZWRfYm9vdHN0cmFwX2J1bmRs'
    'ZRgPIAEoCzIoLmh5ZHJhLmxvY2FsLnYxLkdlbmVyYXRlZEJvb3RzdHJhcEJ1bmRsZUgAUhhnZW'
    '5lcmF0ZWRCb290c3RyYXBCdW5kbGUSVQoTYXZhaWxhYmxlX2NvdW50cmllcxgQIAEoCzIiLmh5'
    'ZHJhLmxvY2FsLnYxLkF2YWlsYWJsZUNvdW50cmllc0gAUhJhdmFpbGFibGVDb3VudHJpZXMSQw'
    'oNdHJhZmZpY19zdGF0cxgRIAEoCzIcLmh5ZHJhLmxvY2FsLnYxLlRyYWZmaWNTdGF0c0gAUgx0'
    'cmFmZmljU3RhdHNCCQoHcGF5bG9hZA==');

@$core.Deprecated('Use trafficStatsDescriptor instead')
const TrafficStats$json = {
  '1': 'TrafficStats',
  '2': [
    {'1': 'session_id', '3': 1, '4': 1, '5': 4, '10': 'sessionId'},
    {
      '1': 'sampled_at_unix_ms',
      '3': 2,
      '4': 1,
      '5': 4,
      '10': 'sampledAtUnixMs'
    },
    {
      '1': 'clients',
      '3': 3,
      '4': 1,
      '5': 13,
      '9': 0,
      '10': 'clients',
      '17': true
    },
    {
      '1': 'my_upload_bytes',
      '3': 4,
      '4': 1,
      '5': 4,
      '9': 1,
      '10': 'myUploadBytes',
      '17': true
    },
    {
      '1': 'shared_upload_bytes',
      '3': 5,
      '4': 1,
      '5': 4,
      '9': 2,
      '10': 'sharedUploadBytes',
      '17': true
    },
    {'1': 'complete', '3': 6, '4': 1, '5': 8, '10': 'complete'},
    {'1': 'active', '3': 7, '4': 1, '5': 8, '10': 'active'},
    {
      '1': 'my_download_bytes',
      '3': 8,
      '4': 1,
      '5': 4,
      '9': 3,
      '10': 'myDownloadBytes',
      '17': true
    },
  ],
  '8': [
    {'1': '_clients'},
    {'1': '_my_upload_bytes'},
    {'1': '_shared_upload_bytes'},
    {'1': '_my_download_bytes'},
  ],
};

/// Descriptor for `TrafficStats`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List trafficStatsDescriptor = $convert.base64Decode(
    'CgxUcmFmZmljU3RhdHMSHQoKc2Vzc2lvbl9pZBgBIAEoBFIJc2Vzc2lvbklkEisKEnNhbXBsZW'
    'RfYXRfdW5peF9tcxgCIAEoBFIPc2FtcGxlZEF0VW5peE1zEh0KB2NsaWVudHMYAyABKA1IAFIH'
    'Y2xpZW50c4gBARIrCg9teV91cGxvYWRfYnl0ZXMYBCABKARIAVINbXlVcGxvYWRCeXRlc4gBAR'
    'IzChNzaGFyZWRfdXBsb2FkX2J5dGVzGAUgASgESAJSEXNoYXJlZFVwbG9hZEJ5dGVziAEBEhoK'
    'CGNvbXBsZXRlGAYgASgIUghjb21wbGV0ZRIWCgZhY3RpdmUYByABKAhSBmFjdGl2ZRIvChFteV'
    '9kb3dubG9hZF9ieXRlcxgIIAEoBEgDUg9teURvd25sb2FkQnl0ZXOIAQFCCgoIX2NsaWVudHNC'
    'EgoQX215X3VwbG9hZF9ieXRlc0IWChRfc2hhcmVkX3VwbG9hZF9ieXRlc0IUChJfbXlfZG93bm'
    'xvYWRfYnl0ZXM=');

@$core.Deprecated('Use statusDescriptor instead')
const Status$json = {
  '1': 'Status',
  '2': [
    {
      '1': 'state',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.hydra.local.v1.ConnectionState',
      '10': 'state'
    },
    {'1': 'country_code', '3': 2, '4': 1, '5': 9, '10': 'countryCode'},
    {
      '1': 'contribution_enabled',
      '3': 3,
      '4': 1,
      '5': 8,
      '10': 'contributionEnabled'
    },
    {
      '1': 'connection',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.ConnectionInfo',
      '10': 'connection'
    },
    {
      '1': 'error',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.UserVisibleError',
      '10': 'error'
    },
  ],
};

/// Descriptor for `Status`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List statusDescriptor = $convert.base64Decode(
    'CgZTdGF0dXMSNQoFc3RhdGUYASABKA4yHy5oeWRyYS5sb2NhbC52MS5Db25uZWN0aW9uU3RhdG'
    'VSBXN0YXRlEiEKDGNvdW50cnlfY29kZRgCIAEoCVILY291bnRyeUNvZGUSMQoUY29udHJpYnV0'
    'aW9uX2VuYWJsZWQYAyABKAhSE2NvbnRyaWJ1dGlvbkVuYWJsZWQSPgoKY29ubmVjdGlvbhgEIA'
    'EoCzIeLmh5ZHJhLmxvY2FsLnYxLkNvbm5lY3Rpb25JbmZvUgpjb25uZWN0aW9uEjYKBWVycm9y'
    'GAUgASgLMiAuaHlkcmEubG9jYWwudjEuVXNlclZpc2libGVFcnJvclIFZXJyb3I=');

@$core.Deprecated('Use connectionInfoDescriptor instead')
const ConnectionInfo$json = {
  '1': 'ConnectionInfo',
  '2': [
    {'1': 'exit_country_code', '3': 1, '4': 1, '5': 9, '10': 'exitCountryCode'},
    {
      '1': 'latency_ms',
      '3': 2,
      '4': 1,
      '5': 13,
      '9': 0,
      '10': 'latencyMs',
      '17': true
    },
    {
      '1': 'connected_since_unix_ms',
      '3': 3,
      '4': 1,
      '5': 4,
      '10': 'connectedSinceUnixMs'
    },
  ],
  '8': [
    {'1': '_latency_ms'},
  ],
};

/// Descriptor for `ConnectionInfo`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List connectionInfoDescriptor = $convert.base64Decode(
    'Cg5Db25uZWN0aW9uSW5mbxIqChFleGl0X2NvdW50cnlfY29kZRgBIAEoCVIPZXhpdENvdW50cn'
    'lDb2RlEiIKCmxhdGVuY3lfbXMYAiABKA1IAFIJbGF0ZW5jeU1ziAEBEjUKF2Nvbm5lY3RlZF9z'
    'aW5jZV91bml4X21zGAMgASgEUhRjb25uZWN0ZWRTaW5jZVVuaXhNc0INCgtfbGF0ZW5jeV9tcw'
    '==');

@$core.Deprecated('Use userVisibleErrorDescriptor instead')
const UserVisibleError$json = {
  '1': 'UserVisibleError',
  '2': [
    {
      '1': 'code',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.hydra.local.v1.ResultCode',
      '10': 'code'
    },
    {'1': 'user_message', '3': 2, '4': 1, '5': 9, '10': 'userMessage'},
  ],
};

/// Descriptor for `UserVisibleError`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List userVisibleErrorDescriptor = $convert.base64Decode(
    'ChBVc2VyVmlzaWJsZUVycm9yEi4KBGNvZGUYASABKA4yGi5oeWRyYS5sb2NhbC52MS5SZXN1bH'
    'RDb2RlUgRjb2RlEiEKDHVzZXJfbWVzc2FnZRgCIAEoCVILdXNlck1lc3NhZ2U=');

@$core.Deprecated('Use contributionDescriptor instead')
const Contribution$json = {
  '1': 'Contribution',
  '2': [
    {'1': 'enabled', '3': 1, '4': 1, '5': 8, '10': 'enabled'},
    {'1': 'supported', '3': 2, '4': 1, '5': 8, '10': 'supported'},
  ],
};

/// Descriptor for `Contribution`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List contributionDescriptor = $convert.base64Decode(
    'CgxDb250cmlidXRpb24SGAoHZW5hYmxlZBgBIAEoCFIHZW5hYmxlZBIcCglzdXBwb3J0ZWQYAi'
    'ABKAhSCXN1cHBvcnRlZA==');

@$core.Deprecated('Use getRecentLogsRequestDescriptor instead')
const GetRecentLogsRequest$json = {
  '1': 'GetRecentLogsRequest',
  '2': [
    {'1': 'limit', '3': 1, '4': 1, '5': 13, '10': 'limit'},
  ],
};

/// Descriptor for `GetRecentLogsRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getRecentLogsRequestDescriptor =
    $convert.base64Decode(
        'ChRHZXRSZWNlbnRMb2dzUmVxdWVzdBIUCgVsaW1pdBgBIAEoDVIFbGltaXQ=');

@$core.Deprecated('Use subscribeLogsRequestDescriptor instead')
const SubscribeLogsRequest$json = {
  '1': 'SubscribeLogsRequest',
  '2': [
    {
      '1': 'daemon_incarnation',
      '3': 1,
      '4': 1,
      '5': 12,
      '10': 'daemonIncarnation'
    },
    {'1': 'after_sequence', '3': 2, '4': 1, '5': 4, '10': 'afterSequence'},
  ],
};

/// Descriptor for `SubscribeLogsRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List subscribeLogsRequestDescriptor = $convert.base64Decode(
    'ChRTdWJzY3JpYmVMb2dzUmVxdWVzdBItChJkYWVtb25faW5jYXJuYXRpb24YASABKAxSEWRhZW'
    '1vbkluY2FybmF0aW9uEiUKDmFmdGVyX3NlcXVlbmNlGAIgASgEUg1hZnRlclNlcXVlbmNl');

@$core.Deprecated('Use logRecordDescriptor instead')
const LogRecord$json = {
  '1': 'LogRecord',
  '2': [
    {'1': 'sequence', '3': 1, '4': 1, '5': 4, '10': 'sequence'},
    {'1': 'timestamp_unix_ms', '3': 2, '4': 1, '5': 4, '10': 'timestampUnixMs'},
    {
      '1': 'severity',
      '3': 3,
      '4': 1,
      '5': 14,
      '6': '.hydra.local.v1.LogSeverity',
      '10': 'severity'
    },
    {'1': 'message', '3': 4, '4': 1, '5': 9, '10': 'message'},
    {
      '1': 'fields',
      '3': 5,
      '4': 3,
      '5': 11,
      '6': '.hydra.local.v1.LogRecord.FieldsEntry',
      '10': 'fields'
    },
  ],
  '3': [LogRecord_FieldsEntry$json],
};

@$core.Deprecated('Use logRecordDescriptor instead')
const LogRecord_FieldsEntry$json = {
  '1': 'FieldsEntry',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
    {'1': 'value', '3': 2, '4': 1, '5': 9, '10': 'value'},
  ],
  '7': {'7': true},
};

/// Descriptor for `LogRecord`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List logRecordDescriptor = $convert.base64Decode(
    'CglMb2dSZWNvcmQSGgoIc2VxdWVuY2UYASABKARSCHNlcXVlbmNlEioKEXRpbWVzdGFtcF91bm'
    'l4X21zGAIgASgEUg90aW1lc3RhbXBVbml4TXMSNwoIc2V2ZXJpdHkYAyABKA4yGy5oeWRyYS5s'
    'b2NhbC52MS5Mb2dTZXZlcml0eVIIc2V2ZXJpdHkSGAoHbWVzc2FnZRgEIAEoCVIHbWVzc2FnZR'
    'I9CgZmaWVsZHMYBSADKAsyJS5oeWRyYS5sb2NhbC52MS5Mb2dSZWNvcmQuRmllbGRzRW50cnlS'
    'BmZpZWxkcxo5CgtGaWVsZHNFbnRyeRIQCgNrZXkYASABKAlSA2tleRIUCgV2YWx1ZRgCIAEoCV'
    'IFdmFsdWU6AjgB');

@$core.Deprecated('Use logTailDescriptor instead')
const LogTail$json = {
  '1': 'LogTail',
  '2': [
    {
      '1': 'records',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.hydra.local.v1.LogRecord',
      '10': 'records'
    },
    {'1': 'last_sequence', '3': 2, '4': 1, '5': 4, '10': 'lastSequence'},
    {
      '1': 'daemon_incarnation',
      '3': 3,
      '4': 1,
      '5': 12,
      '10': 'daemonIncarnation'
    },
    {'1': 'gap', '3': 4, '4': 1, '5': 8, '10': 'gap'},
  ],
};

/// Descriptor for `LogTail`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List logTailDescriptor = $convert.base64Decode(
    'CgdMb2dUYWlsEjMKB3JlY29yZHMYASADKAsyGS5oeWRyYS5sb2NhbC52MS5Mb2dSZWNvcmRSB3'
    'JlY29yZHMSIwoNbGFzdF9zZXF1ZW5jZRgCIAEoBFIMbGFzdFNlcXVlbmNlEi0KEmRhZW1vbl9p'
    'bmNhcm5hdGlvbhgDIAEoDFIRZGFlbW9uSW5jYXJuYXRpb24SEAoDZ2FwGAQgASgIUgNnYXA=');

@$core.Deprecated('Use bootstrapEndpointDescriptor instead')
const BootstrapEndpoint$json = {
  '1': 'BootstrapEndpoint',
  '2': [
    {'1': 'ip', '3': 1, '4': 1, '5': 12, '10': 'ip'},
    {'1': 'port', '3': 2, '4': 1, '5': 13, '10': 'port'},
  ],
};

/// Descriptor for `BootstrapEndpoint`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List bootstrapEndpointDescriptor = $convert.base64Decode(
    'ChFCb290c3RyYXBFbmRwb2ludBIOCgJpcBgBIAEoDFICaXASEgoEcG9ydBgCIAEoDVIEcG9ydA'
    '==');

@$core.Deprecated('Use bootstrapNodeDescriptor instead')
const BootstrapNode$json = {
  '1': 'BootstrapNode',
  '2': [
    {'1': 'peer_id', '3': 1, '4': 1, '5': 9, '10': 'peerId'},
    {
      '1': 'endpoints',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.hydra.local.v1.BootstrapEndpoint',
      '10': 'endpoints'
    },
  ],
};

/// Descriptor for `BootstrapNode`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List bootstrapNodeDescriptor = $convert.base64Decode(
    'Cg1Cb290c3RyYXBOb2RlEhcKB3BlZXJfaWQYASABKAlSBnBlZXJJZBI/CgllbmRwb2ludHMYAi'
    'ADKAsyIS5oeWRyYS5sb2NhbC52MS5Cb290c3RyYXBFbmRwb2ludFIJZW5kcG9pbnRz');

@$core.Deprecated('Use bootstrapBundleDescriptor instead')
const BootstrapBundle$json = {
  '1': 'BootstrapBundle',
  '2': [
    {'1': 'format_version', '3': 1, '4': 1, '5': 13, '10': 'formatVersion'},
    {
      '1': 'generated_at_unix_ms',
      '3': 2,
      '4': 1,
      '5': 4,
      '10': 'generatedAtUnixMs'
    },
    {
      '1': 'nodes',
      '3': 3,
      '4': 3,
      '5': 11,
      '6': '.hydra.local.v1.BootstrapNode',
      '10': 'nodes'
    },
  ],
};

/// Descriptor for `BootstrapBundle`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List bootstrapBundleDescriptor = $convert.base64Decode(
    'Cg9Cb290c3RyYXBCdW5kbGUSJQoOZm9ybWF0X3ZlcnNpb24YASABKA1SDWZvcm1hdFZlcnNpb2'
    '4SLwoUZ2VuZXJhdGVkX2F0X3VuaXhfbXMYAiABKARSEWdlbmVyYXRlZEF0VW5peE1zEjMKBW5v'
    'ZGVzGAMgAygLMh0uaHlkcmEubG9jYWwudjEuQm9vdHN0cmFwTm9kZVIFbm9kZXM=');

@$core.Deprecated('Use bootstrapStateDescriptor instead')
const BootstrapState$json = {
  '1': 'BootstrapState',
  '2': [
    {
      '1': 'phase',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.hydra.local.v1.BootstrapPhase',
      '10': 'phase'
    },
    {'1': 'configured_count', '3': 2, '4': 1, '5': 13, '10': 'configuredCount'},
    {
      '1': 'currently_reachable_count',
      '3': 3,
      '4': 1,
      '5': 13,
      '10': 'currentlyReachableCount'
    },
    {
      '1': 'last_error_code',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.hydra.local.v1.ResultCode',
      '10': 'lastErrorCode'
    },
    {'1': 'user_message', '3': 5, '4': 1, '5': 9, '10': 'userMessage'},
  ],
};

/// Descriptor for `BootstrapState`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List bootstrapStateDescriptor = $convert.base64Decode(
    'Cg5Cb290c3RyYXBTdGF0ZRI0CgVwaGFzZRgBIAEoDjIeLmh5ZHJhLmxvY2FsLnYxLkJvb3RzdH'
    'JhcFBoYXNlUgVwaGFzZRIpChBjb25maWd1cmVkX2NvdW50GAIgASgNUg9jb25maWd1cmVkQ291'
    'bnQSOgoZY3VycmVudGx5X3JlYWNoYWJsZV9jb3VudBgDIAEoDVIXY3VycmVudGx5UmVhY2hhYm'
    'xlQ291bnQSQgoPbGFzdF9lcnJvcl9jb2RlGAQgASgOMhouaHlkcmEubG9jYWwudjEuUmVzdWx0'
    'Q29kZVINbGFzdEVycm9yQ29kZRIhCgx1c2VyX21lc3NhZ2UYBSABKAlSC3VzZXJNZXNzYWdl');

@$core.Deprecated('Use importBootstrapBundleRequestDescriptor instead')
const ImportBootstrapBundleRequest$json = {
  '1': 'ImportBootstrapBundleRequest',
  '2': [
    {'1': 'code', '3': 1, '4': 1, '5': 9, '10': 'code'},
  ],
};

/// Descriptor for `ImportBootstrapBundleRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List importBootstrapBundleRequestDescriptor =
    $convert.base64Decode(
        'ChxJbXBvcnRCb290c3RyYXBCdW5kbGVSZXF1ZXN0EhIKBGNvZGUYASABKAlSBGNvZGU=');

@$core.Deprecated('Use generateBootstrapBundleRequestDescriptor instead')
const GenerateBootstrapBundleRequest$json = {
  '1': 'GenerateBootstrapBundleRequest',
  '2': [
    {
      '1': 'preferred_node_count',
      '3': 1,
      '4': 1,
      '5': 13,
      '10': 'preferredNodeCount'
    },
  ],
};

/// Descriptor for `GenerateBootstrapBundleRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List generateBootstrapBundleRequestDescriptor =
    $convert.base64Decode(
        'Ch5HZW5lcmF0ZUJvb3RzdHJhcEJ1bmRsZVJlcXVlc3QSMAoUcHJlZmVycmVkX25vZGVfY291bn'
        'QYASABKA1SEnByZWZlcnJlZE5vZGVDb3VudA==');

@$core.Deprecated('Use generatedBootstrapBundleDescriptor instead')
const GeneratedBootstrapBundle$json = {
  '1': 'GeneratedBootstrapBundle',
  '2': [
    {'1': 'code', '3': 1, '4': 1, '5': 9, '10': 'code'},
    {'1': 'node_count', '3': 2, '4': 1, '5': 13, '10': 'nodeCount'},
    {
      '1': 'generated_at_unix_ms',
      '3': 3,
      '4': 1,
      '5': 4,
      '10': 'generatedAtUnixMs'
    },
  ],
};

/// Descriptor for `GeneratedBootstrapBundle`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List generatedBootstrapBundleDescriptor = $convert.base64Decode(
    'ChhHZW5lcmF0ZWRCb290c3RyYXBCdW5kbGUSEgoEY29kZRgBIAEoCVIEY29kZRIdCgpub2RlX2'
    'NvdW50GAIgASgNUglub2RlQ291bnQSLwoUZ2VuZXJhdGVkX2F0X3VuaXhfbXMYAyABKARSEWdl'
    'bmVyYXRlZEF0VW5peE1z');

@$core.Deprecated('Use deleteProfileRequestDescriptor instead')
const DeleteProfileRequest$json = {
  '1': 'DeleteProfileRequest',
  '2': [
    {'1': 'confirm', '3': 1, '4': 1, '5': 8, '10': 'confirm'},
  ],
};

/// Descriptor for `DeleteProfileRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteProfileRequestDescriptor =
    $convert.base64Decode(
        'ChREZWxldGVQcm9maWxlUmVxdWVzdBIYCgdjb25maXJtGAEgASgIUgdjb25maXJt');

@$core.Deprecated('Use getAvailableCountriesRequestDescriptor instead')
const GetAvailableCountriesRequest$json = {
  '1': 'GetAvailableCountriesRequest',
  '2': [
    {'1': 'refresh', '3': 1, '4': 1, '5': 8, '10': 'refresh'},
  ],
};

/// Descriptor for `GetAvailableCountriesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getAvailableCountriesRequestDescriptor =
    $convert.base64Decode(
        'ChxHZXRBdmFpbGFibGVDb3VudHJpZXNSZXF1ZXN0EhgKB3JlZnJlc2gYASABKAhSB3JlZnJlc2'
        'g=');

@$core.Deprecated('Use availableCountriesDescriptor instead')
const AvailableCountries$json = {
  '1': 'AvailableCountries',
  '2': [
    {
      '1': 'status',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.hydra.local.v1.AvailableCountriesStatus',
      '10': 'status'
    },
    {'1': 'country_codes', '3': 2, '4': 3, '5': 9, '10': 'countryCodes'},
    {
      '1': 'generated_at_unix_ms',
      '3': 3,
      '4': 1,
      '5': 4,
      '10': 'generatedAtUnixMs'
    },
  ],
};

/// Descriptor for `AvailableCountries`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List availableCountriesDescriptor = $convert.base64Decode(
    'ChJBdmFpbGFibGVDb3VudHJpZXMSQAoGc3RhdHVzGAEgASgOMiguaHlkcmEubG9jYWwudjEuQX'
    'ZhaWxhYmxlQ291bnRyaWVzU3RhdHVzUgZzdGF0dXMSIwoNY291bnRyeV9jb2RlcxgCIAMoCVIM'
    'Y291bnRyeUNvZGVzEi8KFGdlbmVyYXRlZF9hdF91bml4X21zGAMgASgEUhFnZW5lcmF0ZWRBdF'
    'VuaXhNcw==');

@$core.Deprecated('Use eventDescriptor instead')
const Event$json = {
  '1': 'Event',
  '2': [
    {'1': 'sequence', '3': 1, '4': 1, '5': 4, '10': 'sequence'},
    {
      '1': 'status_changed',
      '3': 10,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.StatusChanged',
      '9': 0,
      '10': 'statusChanged'
    },
    {
      '1': 'contribution_changed',
      '3': 11,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.ContributionChanged',
      '9': 0,
      '10': 'contributionChanged'
    },
    {
      '1': 'log_record_added',
      '3': 12,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.LogRecordAdded',
      '9': 0,
      '10': 'logRecordAdded'
    },
  ],
  '8': [
    {'1': 'payload'},
  ],
};

/// Descriptor for `Event`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List eventDescriptor = $convert.base64Decode(
    'CgVFdmVudBIaCghzZXF1ZW5jZRgBIAEoBFIIc2VxdWVuY2USRgoOc3RhdHVzX2NoYW5nZWQYCi'
    'ABKAsyHS5oeWRyYS5sb2NhbC52MS5TdGF0dXNDaGFuZ2VkSABSDXN0YXR1c0NoYW5nZWQSWAoU'
    'Y29udHJpYnV0aW9uX2NoYW5nZWQYCyABKAsyIy5oeWRyYS5sb2NhbC52MS5Db250cmlidXRpb2'
    '5DaGFuZ2VkSABSE2NvbnRyaWJ1dGlvbkNoYW5nZWQSSgoQbG9nX3JlY29yZF9hZGRlZBgMIAEo'
    'CzIeLmh5ZHJhLmxvY2FsLnYxLkxvZ1JlY29yZEFkZGVkSABSDmxvZ1JlY29yZEFkZGVkQgkKB3'
    'BheWxvYWQ=');

@$core.Deprecated('Use statusChangedDescriptor instead')
const StatusChanged$json = {
  '1': 'StatusChanged',
  '2': [
    {
      '1': 'status',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.Status',
      '10': 'status'
    },
  ],
};

/// Descriptor for `StatusChanged`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List statusChangedDescriptor = $convert.base64Decode(
    'Cg1TdGF0dXNDaGFuZ2VkEi4KBnN0YXR1cxgBIAEoCzIWLmh5ZHJhLmxvY2FsLnYxLlN0YXR1c1'
    'IGc3RhdHVz');

@$core.Deprecated('Use contributionChangedDescriptor instead')
const ContributionChanged$json = {
  '1': 'ContributionChanged',
  '2': [
    {
      '1': 'contribution',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.Contribution',
      '10': 'contribution'
    },
  ],
};

/// Descriptor for `ContributionChanged`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List contributionChangedDescriptor = $convert.base64Decode(
    'ChNDb250cmlidXRpb25DaGFuZ2VkEkAKDGNvbnRyaWJ1dGlvbhgBIAEoCzIcLmh5ZHJhLmxvY2'
    'FsLnYxLkNvbnRyaWJ1dGlvblIMY29udHJpYnV0aW9u');

@$core.Deprecated('Use logRecordAddedDescriptor instead')
const LogRecordAdded$json = {
  '1': 'LogRecordAdded',
  '2': [
    {
      '1': 'daemon_incarnation',
      '3': 1,
      '4': 1,
      '5': 12,
      '10': 'daemonIncarnation'
    },
    {
      '1': 'record',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.hydra.local.v1.LogRecord',
      '10': 'record'
    },
  ],
};

/// Descriptor for `LogRecordAdded`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List logRecordAddedDescriptor = $convert.base64Decode(
    'Cg5Mb2dSZWNvcmRBZGRlZBItChJkYWVtb25faW5jYXJuYXRpb24YASABKAxSEWRhZW1vbkluY2'
    'FybmF0aW9uEjEKBnJlY29yZBgCIAEoCzIZLmh5ZHJhLmxvY2FsLnYxLkxvZ1JlY29yZFIGcmVj'
    'b3Jk');
