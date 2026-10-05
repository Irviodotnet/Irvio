// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me
// This is a generated file - do not edit.
//
// Generated from irvio/local/v1/control.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

class Method extends $pb.ProtobufEnum {
  static const Method METHOD_UNSPECIFIED =
      Method._(0, _omitEnumNames ? '' : 'METHOD_UNSPECIFIED');
  static const Method METHOD_HELLO =
      Method._(1, _omitEnumNames ? '' : 'METHOD_HELLO');
  static const Method METHOD_GET_STATUS =
      Method._(2, _omitEnumNames ? '' : 'METHOD_GET_STATUS');
  static const Method METHOD_CONNECT =
      Method._(3, _omitEnumNames ? '' : 'METHOD_CONNECT');
  static const Method METHOD_DISCONNECT =
      Method._(4, _omitEnumNames ? '' : 'METHOD_DISCONNECT');
  static const Method METHOD_GET_CONTRIBUTION =
      Method._(5, _omitEnumNames ? '' : 'METHOD_GET_CONTRIBUTION');
  static const Method METHOD_SET_CONTRIBUTION =
      Method._(6, _omitEnumNames ? '' : 'METHOD_SET_CONTRIBUTION');
  static const Method METHOD_SUBSCRIBE_EVENTS =
      Method._(7, _omitEnumNames ? '' : 'METHOD_SUBSCRIBE_EVENTS');
  static const Method METHOD_GET_RECENT_LOGS =
      Method._(8, _omitEnumNames ? '' : 'METHOD_GET_RECENT_LOGS');
  static const Method METHOD_SUBSCRIBE_LOGS =
      Method._(9, _omitEnumNames ? '' : 'METHOD_SUBSCRIBE_LOGS');
  static const Method METHOD_GET_BOOTSTRAP_STATE =
      Method._(10, _omitEnumNames ? '' : 'METHOD_GET_BOOTSTRAP_STATE');
  static const Method METHOD_IMPORT_BOOTSTRAP_BUNDLE =
      Method._(11, _omitEnumNames ? '' : 'METHOD_IMPORT_BOOTSTRAP_BUNDLE');
  static const Method METHOD_GENERATE_BOOTSTRAP_BUNDLE =
      Method._(12, _omitEnumNames ? '' : 'METHOD_GENERATE_BOOTSTRAP_BUNDLE');
  static const Method METHOD_DELETE_PROFILE =
      Method._(13, _omitEnumNames ? '' : 'METHOD_DELETE_PROFILE');
  static const Method METHOD_GET_AVAILABLE_COUNTRIES =
      Method._(14, _omitEnumNames ? '' : 'METHOD_GET_AVAILABLE_COUNTRIES');
  static const Method METHOD_SHUTDOWN =
      Method._(15, _omitEnumNames ? '' : 'METHOD_SHUTDOWN');
  static const Method METHOD_GET_TRAFFIC_STATS =
      Method._(16, _omitEnumNames ? '' : 'METHOD_GET_TRAFFIC_STATS');

  static const $core.List<Method> values = <Method>[
    METHOD_UNSPECIFIED,
    METHOD_HELLO,
    METHOD_GET_STATUS,
    METHOD_CONNECT,
    METHOD_DISCONNECT,
    METHOD_GET_CONTRIBUTION,
    METHOD_SET_CONTRIBUTION,
    METHOD_SUBSCRIBE_EVENTS,
    METHOD_GET_RECENT_LOGS,
    METHOD_SUBSCRIBE_LOGS,
    METHOD_GET_BOOTSTRAP_STATE,
    METHOD_IMPORT_BOOTSTRAP_BUNDLE,
    METHOD_GENERATE_BOOTSTRAP_BUNDLE,
    METHOD_DELETE_PROFILE,
    METHOD_GET_AVAILABLE_COUNTRIES,
    METHOD_SHUTDOWN,
    METHOD_GET_TRAFFIC_STATS,
  ];

  static final $core.List<Method?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 16);
  static Method? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const Method._(super.value, super.name);
}

class ResultCode extends $pb.ProtobufEnum {
  static const ResultCode RESULT_CODE_UNSPECIFIED =
      ResultCode._(0, _omitEnumNames ? '' : 'RESULT_CODE_UNSPECIFIED');
  static const ResultCode RESULT_CODE_OK =
      ResultCode._(1, _omitEnumNames ? '' : 'RESULT_CODE_OK');
  static const ResultCode RESULT_CODE_INVALID_ARGUMENT =
      ResultCode._(2, _omitEnumNames ? '' : 'RESULT_CODE_INVALID_ARGUMENT');
  static const ResultCode RESULT_CODE_UNSUPPORTED_API =
      ResultCode._(3, _omitEnumNames ? '' : 'RESULT_CODE_UNSUPPORTED_API');
  static const ResultCode RESULT_CODE_UNAUTHORIZED =
      ResultCode._(4, _omitEnumNames ? '' : 'RESULT_CODE_UNAUTHORIZED');
  static const ResultCode RESULT_CODE_BUSY =
      ResultCode._(5, _omitEnumNames ? '' : 'RESULT_CODE_BUSY');
  static const ResultCode RESULT_CODE_NOT_READY =
      ResultCode._(6, _omitEnumNames ? '' : 'RESULT_CODE_NOT_READY');
  static const ResultCode RESULT_CODE_UNSUPPORTED =
      ResultCode._(7, _omitEnumNames ? '' : 'RESULT_CODE_UNSUPPORTED');
  static const ResultCode RESULT_CODE_INTERNAL =
      ResultCode._(8, _omitEnumNames ? '' : 'RESULT_CODE_INTERNAL');

  static const $core.List<ResultCode> values = <ResultCode>[
    RESULT_CODE_UNSPECIFIED,
    RESULT_CODE_OK,
    RESULT_CODE_INVALID_ARGUMENT,
    RESULT_CODE_UNSUPPORTED_API,
    RESULT_CODE_UNAUTHORIZED,
    RESULT_CODE_BUSY,
    RESULT_CODE_NOT_READY,
    RESULT_CODE_UNSUPPORTED,
    RESULT_CODE_INTERNAL,
  ];

  static final $core.List<ResultCode?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 8);
  static ResultCode? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const ResultCode._(super.value, super.name);
}

class ConnectionState extends $pb.ProtobufEnum {
  static const ConnectionState CONNECTION_STATE_UNSPECIFIED = ConnectionState._(
      0, _omitEnumNames ? '' : 'CONNECTION_STATE_UNSPECIFIED');
  static const ConnectionState CONNECTION_STATE_DISCONNECTED =
      ConnectionState._(
          1, _omitEnumNames ? '' : 'CONNECTION_STATE_DISCONNECTED');
  static const ConnectionState CONNECTION_STATE_CONNECTING =
      ConnectionState._(2, _omitEnumNames ? '' : 'CONNECTION_STATE_CONNECTING');
  static const ConnectionState CONNECTION_STATE_CONNECTED =
      ConnectionState._(3, _omitEnumNames ? '' : 'CONNECTION_STATE_CONNECTED');
  static const ConnectionState CONNECTION_STATE_RECOVERING =
      ConnectionState._(4, _omitEnumNames ? '' : 'CONNECTION_STATE_RECOVERING');
  static const ConnectionState CONNECTION_STATE_NO_NETWORK =
      ConnectionState._(5, _omitEnumNames ? '' : 'CONNECTION_STATE_NO_NETWORK');
  static const ConnectionState CONNECTION_STATE_NO_EXIT =
      ConnectionState._(6, _omitEnumNames ? '' : 'CONNECTION_STATE_NO_EXIT');
  static const ConnectionState CONNECTION_STATE_DISCONNECTING =
      ConnectionState._(
          7, _omitEnumNames ? '' : 'CONNECTION_STATE_DISCONNECTING');
  static const ConnectionState CONNECTION_STATE_ERROR =
      ConnectionState._(8, _omitEnumNames ? '' : 'CONNECTION_STATE_ERROR');

  static const $core.List<ConnectionState> values = <ConnectionState>[
    CONNECTION_STATE_UNSPECIFIED,
    CONNECTION_STATE_DISCONNECTED,
    CONNECTION_STATE_CONNECTING,
    CONNECTION_STATE_CONNECTED,
    CONNECTION_STATE_RECOVERING,
    CONNECTION_STATE_NO_NETWORK,
    CONNECTION_STATE_NO_EXIT,
    CONNECTION_STATE_DISCONNECTING,
    CONNECTION_STATE_ERROR,
  ];

  static final $core.List<ConnectionState?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 8);
  static ConnectionState? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const ConnectionState._(super.value, super.name);
}

class LogSeverity extends $pb.ProtobufEnum {
  static const LogSeverity LOG_SEVERITY_UNSPECIFIED =
      LogSeverity._(0, _omitEnumNames ? '' : 'LOG_SEVERITY_UNSPECIFIED');
  static const LogSeverity LOG_SEVERITY_INFO =
      LogSeverity._(1, _omitEnumNames ? '' : 'LOG_SEVERITY_INFO');
  static const LogSeverity LOG_SEVERITY_WARNING =
      LogSeverity._(2, _omitEnumNames ? '' : 'LOG_SEVERITY_WARNING');
  static const LogSeverity LOG_SEVERITY_ERROR =
      LogSeverity._(3, _omitEnumNames ? '' : 'LOG_SEVERITY_ERROR');

  static const $core.List<LogSeverity> values = <LogSeverity>[
    LOG_SEVERITY_UNSPECIFIED,
    LOG_SEVERITY_INFO,
    LOG_SEVERITY_WARNING,
    LOG_SEVERITY_ERROR,
  ];

  static final $core.List<LogSeverity?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static LogSeverity? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const LogSeverity._(super.value, super.name);
}

class BootstrapPhase extends $pb.ProtobufEnum {
  static const BootstrapPhase BOOTSTRAP_PHASE_UNSPECIFIED =
      BootstrapPhase._(0, _omitEnumNames ? '' : 'BOOTSTRAP_PHASE_UNSPECIFIED');
  static const BootstrapPhase BOOTSTRAP_PHASE_EMPTY =
      BootstrapPhase._(1, _omitEnumNames ? '' : 'BOOTSTRAP_PHASE_EMPTY');
  static const BootstrapPhase BOOTSTRAP_PHASE_CONNECTING =
      BootstrapPhase._(2, _omitEnumNames ? '' : 'BOOTSTRAP_PHASE_CONNECTING');
  static const BootstrapPhase BOOTSTRAP_PHASE_READY =
      BootstrapPhase._(3, _omitEnumNames ? '' : 'BOOTSTRAP_PHASE_READY');
  static const BootstrapPhase BOOTSTRAP_PHASE_DEGRADED =
      BootstrapPhase._(4, _omitEnumNames ? '' : 'BOOTSTRAP_PHASE_DEGRADED');
  static const BootstrapPhase BOOTSTRAP_PHASE_FAILED =
      BootstrapPhase._(5, _omitEnumNames ? '' : 'BOOTSTRAP_PHASE_FAILED');

  static const $core.List<BootstrapPhase> values = <BootstrapPhase>[
    BOOTSTRAP_PHASE_UNSPECIFIED,
    BOOTSTRAP_PHASE_EMPTY,
    BOOTSTRAP_PHASE_CONNECTING,
    BOOTSTRAP_PHASE_READY,
    BOOTSTRAP_PHASE_DEGRADED,
    BOOTSTRAP_PHASE_FAILED,
  ];

  static final $core.List<BootstrapPhase?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 5);
  static BootstrapPhase? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const BootstrapPhase._(super.value, super.name);
}

class AvailableCountriesStatus extends $pb.ProtobufEnum {
  static const AvailableCountriesStatus AVAILABLE_COUNTRIES_STATUS_UNSPECIFIED =
      AvailableCountriesStatus._(
          0, _omitEnumNames ? '' : 'AVAILABLE_COUNTRIES_STATUS_UNSPECIFIED');
  static const AvailableCountriesStatus AVAILABLE_COUNTRIES_STATUS_READY =
      AvailableCountriesStatus._(
          1, _omitEnumNames ? '' : 'AVAILABLE_COUNTRIES_STATUS_READY');
  static const AvailableCountriesStatus AVAILABLE_COUNTRIES_STATUS_PARTIAL =
      AvailableCountriesStatus._(
          2, _omitEnumNames ? '' : 'AVAILABLE_COUNTRIES_STATUS_PARTIAL');
  static const AvailableCountriesStatus AVAILABLE_COUNTRIES_STATUS_EMPTY =
      AvailableCountriesStatus._(
          3, _omitEnumNames ? '' : 'AVAILABLE_COUNTRIES_STATUS_EMPTY');
  static const AvailableCountriesStatus AVAILABLE_COUNTRIES_STATUS_UNAVAILABLE =
      AvailableCountriesStatus._(
          4, _omitEnumNames ? '' : 'AVAILABLE_COUNTRIES_STATUS_UNAVAILABLE');

  static const $core.List<AvailableCountriesStatus> values =
      <AvailableCountriesStatus>[
    AVAILABLE_COUNTRIES_STATUS_UNSPECIFIED,
    AVAILABLE_COUNTRIES_STATUS_READY,
    AVAILABLE_COUNTRIES_STATUS_PARTIAL,
    AVAILABLE_COUNTRIES_STATUS_EMPTY,
    AVAILABLE_COUNTRIES_STATUS_UNAVAILABLE,
  ];

  static final $core.List<AvailableCountriesStatus?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 4);
  static AvailableCountriesStatus? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const AvailableCountriesStatus._(super.value, super.name);
}

const $core.bool _omitEnumNames =
    $core.bool.fromEnvironment('protobuf.omit_enum_names');
