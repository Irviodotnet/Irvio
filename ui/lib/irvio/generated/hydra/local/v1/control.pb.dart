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

import 'dart:core' as $core;

import 'package:fixnum/fixnum.dart' as $fixnum;
import 'package:protobuf/protobuf.dart' as $pb;

import 'control.pbenum.dart';

export 'package:protobuf/protobuf.dart' show GeneratedMessageGenericExtensions;

export 'control.pbenum.dart';

enum Envelope_Payload { request, response, event, notSet }

class Envelope extends $pb.GeneratedMessage {
  factory Envelope({
    $core.int? apiMajor,
    $core.int? apiMinor,
    $fixnum.Int64? requestId,
    Request? request,
    Response? response,
    Event? event,
  }) {
    final result = create();
    if (apiMajor != null) result.apiMajor = apiMajor;
    if (apiMinor != null) result.apiMinor = apiMinor;
    if (requestId != null) result.requestId = requestId;
    if (request != null) result.request = request;
    if (response != null) result.response = response;
    if (event != null) result.event = event;
    return result;
  }

  Envelope._();

  factory Envelope.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Envelope.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, Envelope_Payload> _Envelope_PayloadByTag = {
    10: Envelope_Payload.request,
    11: Envelope_Payload.response,
    12: Envelope_Payload.event,
    0: Envelope_Payload.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Envelope',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..oo(0, [10, 11, 12])
    ..aI(1, _omitFieldNames ? '' : 'apiMajor', fieldType: $pb.PbFieldType.OU3)
    ..aI(2, _omitFieldNames ? '' : 'apiMinor', fieldType: $pb.PbFieldType.OU3)
    ..a<$fixnum.Int64>(
        3, _omitFieldNames ? '' : 'requestId', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOM<Request>(10, _omitFieldNames ? '' : 'request',
        subBuilder: Request.create)
    ..aOM<Response>(11, _omitFieldNames ? '' : 'response',
        subBuilder: Response.create)
    ..aOM<Event>(12, _omitFieldNames ? '' : 'event', subBuilder: Event.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Envelope clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Envelope copyWith(void Function(Envelope) updates) =>
      super.copyWith((message) => updates(message as Envelope)) as Envelope;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Envelope create() => Envelope._();
  @$core.override
  Envelope createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static Envelope getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<Envelope>(create);
  static Envelope? _defaultInstance;

  @$pb.TagNumber(10)
  @$pb.TagNumber(11)
  @$pb.TagNumber(12)
  Envelope_Payload whichPayload() => _Envelope_PayloadByTag[$_whichOneof(0)]!;
  @$pb.TagNumber(10)
  @$pb.TagNumber(11)
  @$pb.TagNumber(12)
  void clearPayload() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  $core.int get apiMajor => $_getIZ(0);
  @$pb.TagNumber(1)
  set apiMajor($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasApiMajor() => $_has(0);
  @$pb.TagNumber(1)
  void clearApiMajor() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get apiMinor => $_getIZ(1);
  @$pb.TagNumber(2)
  set apiMinor($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasApiMinor() => $_has(1);
  @$pb.TagNumber(2)
  void clearApiMinor() => $_clearField(2);

  @$pb.TagNumber(3)
  $fixnum.Int64 get requestId => $_getI64(2);
  @$pb.TagNumber(3)
  set requestId($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasRequestId() => $_has(2);
  @$pb.TagNumber(3)
  void clearRequestId() => $_clearField(3);

  @$pb.TagNumber(10)
  Request get request => $_getN(3);
  @$pb.TagNumber(10)
  set request(Request value) => $_setField(10, value);
  @$pb.TagNumber(10)
  $core.bool hasRequest() => $_has(3);
  @$pb.TagNumber(10)
  void clearRequest() => $_clearField(10);
  @$pb.TagNumber(10)
  Request ensureRequest() => $_ensure(3);

  @$pb.TagNumber(11)
  Response get response => $_getN(4);
  @$pb.TagNumber(11)
  set response(Response value) => $_setField(11, value);
  @$pb.TagNumber(11)
  $core.bool hasResponse() => $_has(4);
  @$pb.TagNumber(11)
  void clearResponse() => $_clearField(11);
  @$pb.TagNumber(11)
  Response ensureResponse() => $_ensure(4);

  @$pb.TagNumber(12)
  Event get event => $_getN(5);
  @$pb.TagNumber(12)
  set event(Event value) => $_setField(12, value);
  @$pb.TagNumber(12)
  $core.bool hasEvent() => $_has(5);
  @$pb.TagNumber(12)
  void clearEvent() => $_clearField(12);
  @$pb.TagNumber(12)
  Event ensureEvent() => $_ensure(5);
}

class Request extends $pb.GeneratedMessage {
  factory Request({
    Method? method,
    $core.String? countryCode,
    $core.bool? contributionEnabled,
    GetRecentLogsRequest? getRecentLogs,
    SubscribeLogsRequest? subscribeLogs,
    ImportBootstrapBundleRequest? importBootstrapBundle,
    GenerateBootstrapBundleRequest? generateBootstrapBundle,
    DeleteProfileRequest? deleteProfile,
    GetAvailableCountriesRequest? getAvailableCountries,
  }) {
    final result = create();
    if (method != null) result.method = method;
    if (countryCode != null) result.countryCode = countryCode;
    if (contributionEnabled != null)
      result.contributionEnabled = contributionEnabled;
    if (getRecentLogs != null) result.getRecentLogs = getRecentLogs;
    if (subscribeLogs != null) result.subscribeLogs = subscribeLogs;
    if (importBootstrapBundle != null)
      result.importBootstrapBundle = importBootstrapBundle;
    if (generateBootstrapBundle != null)
      result.generateBootstrapBundle = generateBootstrapBundle;
    if (deleteProfile != null) result.deleteProfile = deleteProfile;
    if (getAvailableCountries != null)
      result.getAvailableCountries = getAvailableCountries;
    return result;
  }

  Request._();

  factory Request.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Request.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Request',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aE<Method>(1, _omitFieldNames ? '' : 'method', enumValues: Method.values)
    ..aOS(2, _omitFieldNames ? '' : 'countryCode')
    ..aOB(3, _omitFieldNames ? '' : 'contributionEnabled')
    ..aOM<GetRecentLogsRequest>(10, _omitFieldNames ? '' : 'getRecentLogs',
        subBuilder: GetRecentLogsRequest.create)
    ..aOM<SubscribeLogsRequest>(11, _omitFieldNames ? '' : 'subscribeLogs',
        subBuilder: SubscribeLogsRequest.create)
    ..aOM<ImportBootstrapBundleRequest>(
        12, _omitFieldNames ? '' : 'importBootstrapBundle',
        subBuilder: ImportBootstrapBundleRequest.create)
    ..aOM<GenerateBootstrapBundleRequest>(
        13, _omitFieldNames ? '' : 'generateBootstrapBundle',
        subBuilder: GenerateBootstrapBundleRequest.create)
    ..aOM<DeleteProfileRequest>(14, _omitFieldNames ? '' : 'deleteProfile',
        subBuilder: DeleteProfileRequest.create)
    ..aOM<GetAvailableCountriesRequest>(
        15, _omitFieldNames ? '' : 'getAvailableCountries',
        subBuilder: GetAvailableCountriesRequest.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Request clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Request copyWith(void Function(Request) updates) =>
      super.copyWith((message) => updates(message as Request)) as Request;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Request create() => Request._();
  @$core.override
  Request createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static Request getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<Request>(create);
  static Request? _defaultInstance;

  @$pb.TagNumber(1)
  Method get method => $_getN(0);
  @$pb.TagNumber(1)
  set method(Method value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasMethod() => $_has(0);
  @$pb.TagNumber(1)
  void clearMethod() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get countryCode => $_getSZ(1);
  @$pb.TagNumber(2)
  set countryCode($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCountryCode() => $_has(1);
  @$pb.TagNumber(2)
  void clearCountryCode() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.bool get contributionEnabled => $_getBF(2);
  @$pb.TagNumber(3)
  set contributionEnabled($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasContributionEnabled() => $_has(2);
  @$pb.TagNumber(3)
  void clearContributionEnabled() => $_clearField(3);

  @$pb.TagNumber(10)
  GetRecentLogsRequest get getRecentLogs => $_getN(3);
  @$pb.TagNumber(10)
  set getRecentLogs(GetRecentLogsRequest value) => $_setField(10, value);
  @$pb.TagNumber(10)
  $core.bool hasGetRecentLogs() => $_has(3);
  @$pb.TagNumber(10)
  void clearGetRecentLogs() => $_clearField(10);
  @$pb.TagNumber(10)
  GetRecentLogsRequest ensureGetRecentLogs() => $_ensure(3);

  @$pb.TagNumber(11)
  SubscribeLogsRequest get subscribeLogs => $_getN(4);
  @$pb.TagNumber(11)
  set subscribeLogs(SubscribeLogsRequest value) => $_setField(11, value);
  @$pb.TagNumber(11)
  $core.bool hasSubscribeLogs() => $_has(4);
  @$pb.TagNumber(11)
  void clearSubscribeLogs() => $_clearField(11);
  @$pb.TagNumber(11)
  SubscribeLogsRequest ensureSubscribeLogs() => $_ensure(4);

  @$pb.TagNumber(12)
  ImportBootstrapBundleRequest get importBootstrapBundle => $_getN(5);
  @$pb.TagNumber(12)
  set importBootstrapBundle(ImportBootstrapBundleRequest value) =>
      $_setField(12, value);
  @$pb.TagNumber(12)
  $core.bool hasImportBootstrapBundle() => $_has(5);
  @$pb.TagNumber(12)
  void clearImportBootstrapBundle() => $_clearField(12);
  @$pb.TagNumber(12)
  ImportBootstrapBundleRequest ensureImportBootstrapBundle() => $_ensure(5);

  @$pb.TagNumber(13)
  GenerateBootstrapBundleRequest get generateBootstrapBundle => $_getN(6);
  @$pb.TagNumber(13)
  set generateBootstrapBundle(GenerateBootstrapBundleRequest value) =>
      $_setField(13, value);
  @$pb.TagNumber(13)
  $core.bool hasGenerateBootstrapBundle() => $_has(6);
  @$pb.TagNumber(13)
  void clearGenerateBootstrapBundle() => $_clearField(13);
  @$pb.TagNumber(13)
  GenerateBootstrapBundleRequest ensureGenerateBootstrapBundle() => $_ensure(6);

  @$pb.TagNumber(14)
  DeleteProfileRequest get deleteProfile => $_getN(7);
  @$pb.TagNumber(14)
  set deleteProfile(DeleteProfileRequest value) => $_setField(14, value);
  @$pb.TagNumber(14)
  $core.bool hasDeleteProfile() => $_has(7);
  @$pb.TagNumber(14)
  void clearDeleteProfile() => $_clearField(14);
  @$pb.TagNumber(14)
  DeleteProfileRequest ensureDeleteProfile() => $_ensure(7);

  @$pb.TagNumber(15)
  GetAvailableCountriesRequest get getAvailableCountries => $_getN(8);
  @$pb.TagNumber(15)
  set getAvailableCountries(GetAvailableCountriesRequest value) =>
      $_setField(15, value);
  @$pb.TagNumber(15)
  $core.bool hasGetAvailableCountries() => $_has(8);
  @$pb.TagNumber(15)
  void clearGetAvailableCountries() => $_clearField(15);
  @$pb.TagNumber(15)
  GetAvailableCountriesRequest ensureGetAvailableCountries() => $_ensure(8);
}

class Hello extends $pb.GeneratedMessage {
  factory Hello({
    $core.int? serverApiMajor,
    $core.int? serverApiMinor,
    $core.int? negotiatedApiMinor,
    $core.String? productVersion,
    $core.String? coreVersion,
  }) {
    final result = create();
    if (serverApiMajor != null) result.serverApiMajor = serverApiMajor;
    if (serverApiMinor != null) result.serverApiMinor = serverApiMinor;
    if (negotiatedApiMinor != null)
      result.negotiatedApiMinor = negotiatedApiMinor;
    if (productVersion != null) result.productVersion = productVersion;
    if (coreVersion != null) result.coreVersion = coreVersion;
    return result;
  }

  Hello._();

  factory Hello.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Hello.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Hello',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'serverApiMajor',
        fieldType: $pb.PbFieldType.OU3)
    ..aI(2, _omitFieldNames ? '' : 'serverApiMinor',
        fieldType: $pb.PbFieldType.OU3)
    ..aI(3, _omitFieldNames ? '' : 'negotiatedApiMinor',
        fieldType: $pb.PbFieldType.OU3)
    ..aOS(4, _omitFieldNames ? '' : 'productVersion')
    ..aOS(5, _omitFieldNames ? '' : 'coreVersion')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Hello clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Hello copyWith(void Function(Hello) updates) =>
      super.copyWith((message) => updates(message as Hello)) as Hello;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Hello create() => Hello._();
  @$core.override
  Hello createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static Hello getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<Hello>(create);
  static Hello? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get serverApiMajor => $_getIZ(0);
  @$pb.TagNumber(1)
  set serverApiMajor($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasServerApiMajor() => $_has(0);
  @$pb.TagNumber(1)
  void clearServerApiMajor() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get serverApiMinor => $_getIZ(1);
  @$pb.TagNumber(2)
  set serverApiMinor($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasServerApiMinor() => $_has(1);
  @$pb.TagNumber(2)
  void clearServerApiMinor() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.int get negotiatedApiMinor => $_getIZ(2);
  @$pb.TagNumber(3)
  set negotiatedApiMinor($core.int value) => $_setUnsignedInt32(2, value);
  @$pb.TagNumber(3)
  $core.bool hasNegotiatedApiMinor() => $_has(2);
  @$pb.TagNumber(3)
  void clearNegotiatedApiMinor() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get productVersion => $_getSZ(3);
  @$pb.TagNumber(4)
  set productVersion($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasProductVersion() => $_has(3);
  @$pb.TagNumber(4)
  void clearProductVersion() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.String get coreVersion => $_getSZ(4);
  @$pb.TagNumber(5)
  set coreVersion($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasCoreVersion() => $_has(4);
  @$pb.TagNumber(5)
  void clearCoreVersion() => $_clearField(5);
}

enum Response_Payload {
  hello,
  status,
  contribution,
  logTail,
  bootstrapState,
  generatedBootstrapBundle,
  availableCountries,
  trafficStats,
  notSet
}

class Response extends $pb.GeneratedMessage {
  factory Response({
    ResultCode? code,
    $core.String? userMessage,
    $core.bool? accepted,
    Hello? hello,
    Status? status,
    Contribution? contribution,
    LogTail? logTail,
    BootstrapState? bootstrapState,
    GeneratedBootstrapBundle? generatedBootstrapBundle,
    AvailableCountries? availableCountries,
    TrafficStats? trafficStats,
  }) {
    final result = create();
    if (code != null) result.code = code;
    if (userMessage != null) result.userMessage = userMessage;
    if (accepted != null) result.accepted = accepted;
    if (hello != null) result.hello = hello;
    if (status != null) result.status = status;
    if (contribution != null) result.contribution = contribution;
    if (logTail != null) result.logTail = logTail;
    if (bootstrapState != null) result.bootstrapState = bootstrapState;
    if (generatedBootstrapBundle != null)
      result.generatedBootstrapBundle = generatedBootstrapBundle;
    if (availableCountries != null)
      result.availableCountries = availableCountries;
    if (trafficStats != null) result.trafficStats = trafficStats;
    return result;
  }

  Response._();

  factory Response.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Response.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, Response_Payload> _Response_PayloadByTag = {
    10: Response_Payload.hello,
    11: Response_Payload.status,
    12: Response_Payload.contribution,
    13: Response_Payload.logTail,
    14: Response_Payload.bootstrapState,
    15: Response_Payload.generatedBootstrapBundle,
    16: Response_Payload.availableCountries,
    17: Response_Payload.trafficStats,
    0: Response_Payload.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Response',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..oo(0, [10, 11, 12, 13, 14, 15, 16, 17])
    ..aE<ResultCode>(1, _omitFieldNames ? '' : 'code',
        enumValues: ResultCode.values)
    ..aOS(2, _omitFieldNames ? '' : 'userMessage')
    ..aOB(3, _omitFieldNames ? '' : 'accepted')
    ..aOM<Hello>(10, _omitFieldNames ? '' : 'hello', subBuilder: Hello.create)
    ..aOM<Status>(11, _omitFieldNames ? '' : 'status',
        subBuilder: Status.create)
    ..aOM<Contribution>(12, _omitFieldNames ? '' : 'contribution',
        subBuilder: Contribution.create)
    ..aOM<LogTail>(13, _omitFieldNames ? '' : 'logTail',
        subBuilder: LogTail.create)
    ..aOM<BootstrapState>(14, _omitFieldNames ? '' : 'bootstrapState',
        subBuilder: BootstrapState.create)
    ..aOM<GeneratedBootstrapBundle>(
        15, _omitFieldNames ? '' : 'generatedBootstrapBundle',
        subBuilder: GeneratedBootstrapBundle.create)
    ..aOM<AvailableCountries>(16, _omitFieldNames ? '' : 'availableCountries',
        subBuilder: AvailableCountries.create)
    ..aOM<TrafficStats>(17, _omitFieldNames ? '' : 'trafficStats',
        subBuilder: TrafficStats.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Response clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Response copyWith(void Function(Response) updates) =>
      super.copyWith((message) => updates(message as Response)) as Response;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Response create() => Response._();
  @$core.override
  Response createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static Response getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<Response>(create);
  static Response? _defaultInstance;

  @$pb.TagNumber(10)
  @$pb.TagNumber(11)
  @$pb.TagNumber(12)
  @$pb.TagNumber(13)
  @$pb.TagNumber(14)
  @$pb.TagNumber(15)
  @$pb.TagNumber(16)
  @$pb.TagNumber(17)
  Response_Payload whichPayload() => _Response_PayloadByTag[$_whichOneof(0)]!;
  @$pb.TagNumber(10)
  @$pb.TagNumber(11)
  @$pb.TagNumber(12)
  @$pb.TagNumber(13)
  @$pb.TagNumber(14)
  @$pb.TagNumber(15)
  @$pb.TagNumber(16)
  @$pb.TagNumber(17)
  void clearPayload() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  ResultCode get code => $_getN(0);
  @$pb.TagNumber(1)
  set code(ResultCode value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasCode() => $_has(0);
  @$pb.TagNumber(1)
  void clearCode() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get userMessage => $_getSZ(1);
  @$pb.TagNumber(2)
  set userMessage($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasUserMessage() => $_has(1);
  @$pb.TagNumber(2)
  void clearUserMessage() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.bool get accepted => $_getBF(2);
  @$pb.TagNumber(3)
  set accepted($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasAccepted() => $_has(2);
  @$pb.TagNumber(3)
  void clearAccepted() => $_clearField(3);

  @$pb.TagNumber(10)
  Hello get hello => $_getN(3);
  @$pb.TagNumber(10)
  set hello(Hello value) => $_setField(10, value);
  @$pb.TagNumber(10)
  $core.bool hasHello() => $_has(3);
  @$pb.TagNumber(10)
  void clearHello() => $_clearField(10);
  @$pb.TagNumber(10)
  Hello ensureHello() => $_ensure(3);

  @$pb.TagNumber(11)
  Status get status => $_getN(4);
  @$pb.TagNumber(11)
  set status(Status value) => $_setField(11, value);
  @$pb.TagNumber(11)
  $core.bool hasStatus() => $_has(4);
  @$pb.TagNumber(11)
  void clearStatus() => $_clearField(11);
  @$pb.TagNumber(11)
  Status ensureStatus() => $_ensure(4);

  @$pb.TagNumber(12)
  Contribution get contribution => $_getN(5);
  @$pb.TagNumber(12)
  set contribution(Contribution value) => $_setField(12, value);
  @$pb.TagNumber(12)
  $core.bool hasContribution() => $_has(5);
  @$pb.TagNumber(12)
  void clearContribution() => $_clearField(12);
  @$pb.TagNumber(12)
  Contribution ensureContribution() => $_ensure(5);

  @$pb.TagNumber(13)
  LogTail get logTail => $_getN(6);
  @$pb.TagNumber(13)
  set logTail(LogTail value) => $_setField(13, value);
  @$pb.TagNumber(13)
  $core.bool hasLogTail() => $_has(6);
  @$pb.TagNumber(13)
  void clearLogTail() => $_clearField(13);
  @$pb.TagNumber(13)
  LogTail ensureLogTail() => $_ensure(6);

  @$pb.TagNumber(14)
  BootstrapState get bootstrapState => $_getN(7);
  @$pb.TagNumber(14)
  set bootstrapState(BootstrapState value) => $_setField(14, value);
  @$pb.TagNumber(14)
  $core.bool hasBootstrapState() => $_has(7);
  @$pb.TagNumber(14)
  void clearBootstrapState() => $_clearField(14);
  @$pb.TagNumber(14)
  BootstrapState ensureBootstrapState() => $_ensure(7);

  @$pb.TagNumber(15)
  GeneratedBootstrapBundle get generatedBootstrapBundle => $_getN(8);
  @$pb.TagNumber(15)
  set generatedBootstrapBundle(GeneratedBootstrapBundle value) =>
      $_setField(15, value);
  @$pb.TagNumber(15)
  $core.bool hasGeneratedBootstrapBundle() => $_has(8);
  @$pb.TagNumber(15)
  void clearGeneratedBootstrapBundle() => $_clearField(15);
  @$pb.TagNumber(15)
  GeneratedBootstrapBundle ensureGeneratedBootstrapBundle() => $_ensure(8);

  @$pb.TagNumber(16)
  AvailableCountries get availableCountries => $_getN(9);
  @$pb.TagNumber(16)
  set availableCountries(AvailableCountries value) => $_setField(16, value);
  @$pb.TagNumber(16)
  $core.bool hasAvailableCountries() => $_has(9);
  @$pb.TagNumber(16)
  void clearAvailableCountries() => $_clearField(16);
  @$pb.TagNumber(16)
  AvailableCountries ensureAvailableCountries() => $_ensure(9);

  @$pb.TagNumber(17)
  TrafficStats get trafficStats => $_getN(10);
  @$pb.TagNumber(17)
  set trafficStats(TrafficStats value) => $_setField(17, value);
  @$pb.TagNumber(17)
  $core.bool hasTrafficStats() => $_has(10);
  @$pb.TagNumber(17)
  void clearTrafficStats() => $_clearField(17);
  @$pb.TagNumber(17)
  TrafficStats ensureTrafficStats() => $_ensure(10);
}

/// Local-only aggregate. Missing optional values are unavailable, not zero.
class TrafficStats extends $pb.GeneratedMessage {
  factory TrafficStats({
    $fixnum.Int64? sessionId,
    $fixnum.Int64? sampledAtUnixMs,
    $core.int? clients,
    $fixnum.Int64? myUploadBytes,
    $fixnum.Int64? sharedUploadBytes,
    $core.bool? complete,
    $core.bool? active,
    $fixnum.Int64? myDownloadBytes,
  }) {
    final result = create();
    if (sessionId != null) result.sessionId = sessionId;
    if (sampledAtUnixMs != null) result.sampledAtUnixMs = sampledAtUnixMs;
    if (clients != null) result.clients = clients;
    if (myUploadBytes != null) result.myUploadBytes = myUploadBytes;
    if (sharedUploadBytes != null) result.sharedUploadBytes = sharedUploadBytes;
    if (complete != null) result.complete = complete;
    if (active != null) result.active = active;
    if (myDownloadBytes != null) result.myDownloadBytes = myDownloadBytes;
    return result;
  }

  TrafficStats._();

  factory TrafficStats.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory TrafficStats.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'TrafficStats',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(
        1, _omitFieldNames ? '' : 'sessionId', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'sampledAtUnixMs', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aI(3, _omitFieldNames ? '' : 'clients', fieldType: $pb.PbFieldType.OU3)
    ..a<$fixnum.Int64>(
        4, _omitFieldNames ? '' : 'myUploadBytes', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        5, _omitFieldNames ? '' : 'sharedUploadBytes', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOB(6, _omitFieldNames ? '' : 'complete')
    ..aOB(7, _omitFieldNames ? '' : 'active')
    ..a<$fixnum.Int64>(
        8, _omitFieldNames ? '' : 'myDownloadBytes', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  TrafficStats clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  TrafficStats copyWith(void Function(TrafficStats) updates) =>
      super.copyWith((message) => updates(message as TrafficStats))
          as TrafficStats;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static TrafficStats create() => TrafficStats._();
  @$core.override
  TrafficStats createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static TrafficStats getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<TrafficStats>(create);
  static TrafficStats? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get sessionId => $_getI64(0);
  @$pb.TagNumber(1)
  set sessionId($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSessionId() => $_has(0);
  @$pb.TagNumber(1)
  void clearSessionId() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get sampledAtUnixMs => $_getI64(1);
  @$pb.TagNumber(2)
  set sampledAtUnixMs($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasSampledAtUnixMs() => $_has(1);
  @$pb.TagNumber(2)
  void clearSampledAtUnixMs() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.int get clients => $_getIZ(2);
  @$pb.TagNumber(3)
  set clients($core.int value) => $_setUnsignedInt32(2, value);
  @$pb.TagNumber(3)
  $core.bool hasClients() => $_has(2);
  @$pb.TagNumber(3)
  void clearClients() => $_clearField(3);

  @$pb.TagNumber(4)
  $fixnum.Int64 get myUploadBytes => $_getI64(3);
  @$pb.TagNumber(4)
  set myUploadBytes($fixnum.Int64 value) => $_setInt64(3, value);
  @$pb.TagNumber(4)
  $core.bool hasMyUploadBytes() => $_has(3);
  @$pb.TagNumber(4)
  void clearMyUploadBytes() => $_clearField(4);

  @$pb.TagNumber(5)
  $fixnum.Int64 get sharedUploadBytes => $_getI64(4);
  @$pb.TagNumber(5)
  set sharedUploadBytes($fixnum.Int64 value) => $_setInt64(4, value);
  @$pb.TagNumber(5)
  $core.bool hasSharedUploadBytes() => $_has(4);
  @$pb.TagNumber(5)
  void clearSharedUploadBytes() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.bool get complete => $_getBF(5);
  @$pb.TagNumber(6)
  set complete($core.bool value) => $_setBool(5, value);
  @$pb.TagNumber(6)
  $core.bool hasComplete() => $_has(5);
  @$pb.TagNumber(6)
  void clearComplete() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.bool get active => $_getBF(6);
  @$pb.TagNumber(7)
  set active($core.bool value) => $_setBool(6, value);
  @$pb.TagNumber(7)
  $core.bool hasActive() => $_has(6);
  @$pb.TagNumber(7)
  void clearActive() => $_clearField(7);

  @$pb.TagNumber(8)
  $fixnum.Int64 get myDownloadBytes => $_getI64(7);
  @$pb.TagNumber(8)
  set myDownloadBytes($fixnum.Int64 value) => $_setInt64(7, value);
  @$pb.TagNumber(8)
  $core.bool hasMyDownloadBytes() => $_has(7);
  @$pb.TagNumber(8)
  void clearMyDownloadBytes() => $_clearField(8);
}

class Status extends $pb.GeneratedMessage {
  factory Status({
    ConnectionState? state,
    $core.String? countryCode,
    $core.bool? contributionEnabled,
    ConnectionInfo? connection,
    UserVisibleError? error,
  }) {
    final result = create();
    if (state != null) result.state = state;
    if (countryCode != null) result.countryCode = countryCode;
    if (contributionEnabled != null)
      result.contributionEnabled = contributionEnabled;
    if (connection != null) result.connection = connection;
    if (error != null) result.error = error;
    return result;
  }

  Status._();

  factory Status.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Status.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Status',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aE<ConnectionState>(1, _omitFieldNames ? '' : 'state',
        enumValues: ConnectionState.values)
    ..aOS(2, _omitFieldNames ? '' : 'countryCode')
    ..aOB(3, _omitFieldNames ? '' : 'contributionEnabled')
    ..aOM<ConnectionInfo>(4, _omitFieldNames ? '' : 'connection',
        subBuilder: ConnectionInfo.create)
    ..aOM<UserVisibleError>(5, _omitFieldNames ? '' : 'error',
        subBuilder: UserVisibleError.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Status clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Status copyWith(void Function(Status) updates) =>
      super.copyWith((message) => updates(message as Status)) as Status;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Status create() => Status._();
  @$core.override
  Status createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static Status getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<Status>(create);
  static Status? _defaultInstance;

  @$pb.TagNumber(1)
  ConnectionState get state => $_getN(0);
  @$pb.TagNumber(1)
  set state(ConnectionState value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasState() => $_has(0);
  @$pb.TagNumber(1)
  void clearState() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get countryCode => $_getSZ(1);
  @$pb.TagNumber(2)
  set countryCode($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCountryCode() => $_has(1);
  @$pb.TagNumber(2)
  void clearCountryCode() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.bool get contributionEnabled => $_getBF(2);
  @$pb.TagNumber(3)
  set contributionEnabled($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasContributionEnabled() => $_has(2);
  @$pb.TagNumber(3)
  void clearContributionEnabled() => $_clearField(3);

  @$pb.TagNumber(4)
  ConnectionInfo get connection => $_getN(3);
  @$pb.TagNumber(4)
  set connection(ConnectionInfo value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasConnection() => $_has(3);
  @$pb.TagNumber(4)
  void clearConnection() => $_clearField(4);
  @$pb.TagNumber(4)
  ConnectionInfo ensureConnection() => $_ensure(3);

  @$pb.TagNumber(5)
  UserVisibleError get error => $_getN(4);
  @$pb.TagNumber(5)
  set error(UserVisibleError value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasError() => $_has(4);
  @$pb.TagNumber(5)
  void clearError() => $_clearField(5);
  @$pb.TagNumber(5)
  UserVisibleError ensureError() => $_ensure(4);
}

class ConnectionInfo extends $pb.GeneratedMessage {
  factory ConnectionInfo({
    $core.String? exitCountryCode,
    $core.int? latencyMs,
    $fixnum.Int64? connectedSinceUnixMs,
  }) {
    final result = create();
    if (exitCountryCode != null) result.exitCountryCode = exitCountryCode;
    if (latencyMs != null) result.latencyMs = latencyMs;
    if (connectedSinceUnixMs != null)
      result.connectedSinceUnixMs = connectedSinceUnixMs;
    return result;
  }

  ConnectionInfo._();

  factory ConnectionInfo.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ConnectionInfo.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ConnectionInfo',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'exitCountryCode')
    ..aI(2, _omitFieldNames ? '' : 'latencyMs', fieldType: $pb.PbFieldType.OU3)
    ..a<$fixnum.Int64>(
        3, _omitFieldNames ? '' : 'connectedSinceUnixMs', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ConnectionInfo clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ConnectionInfo copyWith(void Function(ConnectionInfo) updates) =>
      super.copyWith((message) => updates(message as ConnectionInfo))
          as ConnectionInfo;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ConnectionInfo create() => ConnectionInfo._();
  @$core.override
  ConnectionInfo createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ConnectionInfo getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ConnectionInfo>(create);
  static ConnectionInfo? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get exitCountryCode => $_getSZ(0);
  @$pb.TagNumber(1)
  set exitCountryCode($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasExitCountryCode() => $_has(0);
  @$pb.TagNumber(1)
  void clearExitCountryCode() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get latencyMs => $_getIZ(1);
  @$pb.TagNumber(2)
  set latencyMs($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLatencyMs() => $_has(1);
  @$pb.TagNumber(2)
  void clearLatencyMs() => $_clearField(2);

  @$pb.TagNumber(3)
  $fixnum.Int64 get connectedSinceUnixMs => $_getI64(2);
  @$pb.TagNumber(3)
  set connectedSinceUnixMs($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasConnectedSinceUnixMs() => $_has(2);
  @$pb.TagNumber(3)
  void clearConnectedSinceUnixMs() => $_clearField(3);
}

class UserVisibleError extends $pb.GeneratedMessage {
  factory UserVisibleError({
    ResultCode? code,
    $core.String? userMessage,
  }) {
    final result = create();
    if (code != null) result.code = code;
    if (userMessage != null) result.userMessage = userMessage;
    return result;
  }

  UserVisibleError._();

  factory UserVisibleError.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory UserVisibleError.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'UserVisibleError',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aE<ResultCode>(1, _omitFieldNames ? '' : 'code',
        enumValues: ResultCode.values)
    ..aOS(2, _omitFieldNames ? '' : 'userMessage')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  UserVisibleError clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  UserVisibleError copyWith(void Function(UserVisibleError) updates) =>
      super.copyWith((message) => updates(message as UserVisibleError))
          as UserVisibleError;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static UserVisibleError create() => UserVisibleError._();
  @$core.override
  UserVisibleError createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static UserVisibleError getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<UserVisibleError>(create);
  static UserVisibleError? _defaultInstance;

  @$pb.TagNumber(1)
  ResultCode get code => $_getN(0);
  @$pb.TagNumber(1)
  set code(ResultCode value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasCode() => $_has(0);
  @$pb.TagNumber(1)
  void clearCode() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get userMessage => $_getSZ(1);
  @$pb.TagNumber(2)
  set userMessage($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasUserMessage() => $_has(1);
  @$pb.TagNumber(2)
  void clearUserMessage() => $_clearField(2);
}

class Contribution extends $pb.GeneratedMessage {
  factory Contribution({
    $core.bool? enabled,
    $core.bool? supported,
  }) {
    final result = create();
    if (enabled != null) result.enabled = enabled;
    if (supported != null) result.supported = supported;
    return result;
  }

  Contribution._();

  factory Contribution.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Contribution.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Contribution',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'enabled')
    ..aOB(2, _omitFieldNames ? '' : 'supported')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Contribution clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Contribution copyWith(void Function(Contribution) updates) =>
      super.copyWith((message) => updates(message as Contribution))
          as Contribution;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Contribution create() => Contribution._();
  @$core.override
  Contribution createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static Contribution getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<Contribution>(create);
  static Contribution? _defaultInstance;

  @$pb.TagNumber(1)
  $core.bool get enabled => $_getBF(0);
  @$pb.TagNumber(1)
  set enabled($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasEnabled() => $_has(0);
  @$pb.TagNumber(1)
  void clearEnabled() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.bool get supported => $_getBF(1);
  @$pb.TagNumber(2)
  set supported($core.bool value) => $_setBool(1, value);
  @$pb.TagNumber(2)
  $core.bool hasSupported() => $_has(1);
  @$pb.TagNumber(2)
  void clearSupported() => $_clearField(2);
}

class GetRecentLogsRequest extends $pb.GeneratedMessage {
  factory GetRecentLogsRequest({
    $core.int? limit,
  }) {
    final result = create();
    if (limit != null) result.limit = limit;
    return result;
  }

  GetRecentLogsRequest._();

  factory GetRecentLogsRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetRecentLogsRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetRecentLogsRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRecentLogsRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRecentLogsRequest copyWith(void Function(GetRecentLogsRequest) updates) =>
      super.copyWith((message) => updates(message as GetRecentLogsRequest))
          as GetRecentLogsRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetRecentLogsRequest create() => GetRecentLogsRequest._();
  @$core.override
  GetRecentLogsRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetRecentLogsRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetRecentLogsRequest>(create);
  static GetRecentLogsRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get limit => $_getIZ(0);
  @$pb.TagNumber(1)
  set limit($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasLimit() => $_has(0);
  @$pb.TagNumber(1)
  void clearLimit() => $_clearField(1);
}

class SubscribeLogsRequest extends $pb.GeneratedMessage {
  factory SubscribeLogsRequest({
    $core.List<$core.int>? daemonIncarnation,
    $fixnum.Int64? afterSequence,
  }) {
    final result = create();
    if (daemonIncarnation != null) result.daemonIncarnation = daemonIncarnation;
    if (afterSequence != null) result.afterSequence = afterSequence;
    return result;
  }

  SubscribeLogsRequest._();

  factory SubscribeLogsRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SubscribeLogsRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SubscribeLogsRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'daemonIncarnation', $pb.PbFieldType.OY)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'afterSequence', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SubscribeLogsRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SubscribeLogsRequest copyWith(void Function(SubscribeLogsRequest) updates) =>
      super.copyWith((message) => updates(message as SubscribeLogsRequest))
          as SubscribeLogsRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SubscribeLogsRequest create() => SubscribeLogsRequest._();
  @$core.override
  SubscribeLogsRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SubscribeLogsRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SubscribeLogsRequest>(create);
  static SubscribeLogsRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get daemonIncarnation => $_getN(0);
  @$pb.TagNumber(1)
  set daemonIncarnation($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasDaemonIncarnation() => $_has(0);
  @$pb.TagNumber(1)
  void clearDaemonIncarnation() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get afterSequence => $_getI64(1);
  @$pb.TagNumber(2)
  set afterSequence($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasAfterSequence() => $_has(1);
  @$pb.TagNumber(2)
  void clearAfterSequence() => $_clearField(2);
}

class LogRecord extends $pb.GeneratedMessage {
  factory LogRecord({
    $fixnum.Int64? sequence,
    $fixnum.Int64? timestampUnixMs,
    LogSeverity? severity,
    $core.String? message,
    $core.Iterable<$core.MapEntry<$core.String, $core.String>>? fields,
  }) {
    final result = create();
    if (sequence != null) result.sequence = sequence;
    if (timestampUnixMs != null) result.timestampUnixMs = timestampUnixMs;
    if (severity != null) result.severity = severity;
    if (message != null) result.message = message;
    if (fields != null) result.fields.addEntries(fields);
    return result;
  }

  LogRecord._();

  factory LogRecord.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory LogRecord.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'LogRecord',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(
        1, _omitFieldNames ? '' : 'sequence', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'timestampUnixMs', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aE<LogSeverity>(3, _omitFieldNames ? '' : 'severity',
        enumValues: LogSeverity.values)
    ..aOS(4, _omitFieldNames ? '' : 'message')
    ..m<$core.String, $core.String>(5, _omitFieldNames ? '' : 'fields',
        entryClassName: 'LogRecord.FieldsEntry',
        keyFieldType: $pb.PbFieldType.OS,
        valueFieldType: $pb.PbFieldType.OS,
        packageName: const $pb.PackageName('hydra.local.v1'))
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LogRecord clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LogRecord copyWith(void Function(LogRecord) updates) =>
      super.copyWith((message) => updates(message as LogRecord)) as LogRecord;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static LogRecord create() => LogRecord._();
  @$core.override
  LogRecord createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static LogRecord getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<LogRecord>(create);
  static LogRecord? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get sequence => $_getI64(0);
  @$pb.TagNumber(1)
  set sequence($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSequence() => $_has(0);
  @$pb.TagNumber(1)
  void clearSequence() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get timestampUnixMs => $_getI64(1);
  @$pb.TagNumber(2)
  set timestampUnixMs($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasTimestampUnixMs() => $_has(1);
  @$pb.TagNumber(2)
  void clearTimestampUnixMs() => $_clearField(2);

  @$pb.TagNumber(3)
  LogSeverity get severity => $_getN(2);
  @$pb.TagNumber(3)
  set severity(LogSeverity value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasSeverity() => $_has(2);
  @$pb.TagNumber(3)
  void clearSeverity() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get message => $_getSZ(3);
  @$pb.TagNumber(4)
  set message($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasMessage() => $_has(3);
  @$pb.TagNumber(4)
  void clearMessage() => $_clearField(4);

  @$pb.TagNumber(5)
  $pb.PbMap<$core.String, $core.String> get fields => $_getMap(4);
}

class LogTail extends $pb.GeneratedMessage {
  factory LogTail({
    $core.Iterable<LogRecord>? records,
    $fixnum.Int64? lastSequence,
    $core.List<$core.int>? daemonIncarnation,
    $core.bool? gap,
  }) {
    final result = create();
    if (records != null) result.records.addAll(records);
    if (lastSequence != null) result.lastSequence = lastSequence;
    if (daemonIncarnation != null) result.daemonIncarnation = daemonIncarnation;
    if (gap != null) result.gap = gap;
    return result;
  }

  LogTail._();

  factory LogTail.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory LogTail.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'LogTail',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..pPM<LogRecord>(1, _omitFieldNames ? '' : 'records',
        subBuilder: LogRecord.create)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'lastSequence', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'daemonIncarnation', $pb.PbFieldType.OY)
    ..aOB(4, _omitFieldNames ? '' : 'gap')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LogTail clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LogTail copyWith(void Function(LogTail) updates) =>
      super.copyWith((message) => updates(message as LogTail)) as LogTail;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static LogTail create() => LogTail._();
  @$core.override
  LogTail createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static LogTail getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<LogTail>(create);
  static LogTail? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<LogRecord> get records => $_getList(0);

  @$pb.TagNumber(2)
  $fixnum.Int64 get lastSequence => $_getI64(1);
  @$pb.TagNumber(2)
  set lastSequence($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLastSequence() => $_has(1);
  @$pb.TagNumber(2)
  void clearLastSequence() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get daemonIncarnation => $_getN(2);
  @$pb.TagNumber(3)
  set daemonIncarnation($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasDaemonIncarnation() => $_has(2);
  @$pb.TagNumber(3)
  void clearDaemonIncarnation() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.bool get gap => $_getBF(3);
  @$pb.TagNumber(4)
  set gap($core.bool value) => $_setBool(3, value);
  @$pb.TagNumber(4)
  $core.bool hasGap() => $_has(3);
  @$pb.TagNumber(4)
  void clearGap() => $_clearField(4);
}

class BootstrapEndpoint extends $pb.GeneratedMessage {
  factory BootstrapEndpoint({
    $core.List<$core.int>? ip,
    $core.int? port,
  }) {
    final result = create();
    if (ip != null) result.ip = ip;
    if (port != null) result.port = port;
    return result;
  }

  BootstrapEndpoint._();

  factory BootstrapEndpoint.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BootstrapEndpoint.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BootstrapEndpoint',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'ip', $pb.PbFieldType.OY)
    ..aI(2, _omitFieldNames ? '' : 'port', fieldType: $pb.PbFieldType.OU3)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BootstrapEndpoint clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BootstrapEndpoint copyWith(void Function(BootstrapEndpoint) updates) =>
      super.copyWith((message) => updates(message as BootstrapEndpoint))
          as BootstrapEndpoint;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BootstrapEndpoint create() => BootstrapEndpoint._();
  @$core.override
  BootstrapEndpoint createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static BootstrapEndpoint getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BootstrapEndpoint>(create);
  static BootstrapEndpoint? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get ip => $_getN(0);
  @$pb.TagNumber(1)
  set ip($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasIp() => $_has(0);
  @$pb.TagNumber(1)
  void clearIp() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get port => $_getIZ(1);
  @$pb.TagNumber(2)
  set port($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasPort() => $_has(1);
  @$pb.TagNumber(2)
  void clearPort() => $_clearField(2);
}

class BootstrapNode extends $pb.GeneratedMessage {
  factory BootstrapNode({
    $core.String? peerId,
    $core.Iterable<BootstrapEndpoint>? endpoints,
  }) {
    final result = create();
    if (peerId != null) result.peerId = peerId;
    if (endpoints != null) result.endpoints.addAll(endpoints);
    return result;
  }

  BootstrapNode._();

  factory BootstrapNode.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BootstrapNode.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BootstrapNode',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'peerId')
    ..pPM<BootstrapEndpoint>(2, _omitFieldNames ? '' : 'endpoints',
        subBuilder: BootstrapEndpoint.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BootstrapNode clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BootstrapNode copyWith(void Function(BootstrapNode) updates) =>
      super.copyWith((message) => updates(message as BootstrapNode))
          as BootstrapNode;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BootstrapNode create() => BootstrapNode._();
  @$core.override
  BootstrapNode createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static BootstrapNode getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BootstrapNode>(create);
  static BootstrapNode? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get peerId => $_getSZ(0);
  @$pb.TagNumber(1)
  set peerId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPeerId() => $_has(0);
  @$pb.TagNumber(1)
  void clearPeerId() => $_clearField(1);

  @$pb.TagNumber(2)
  $pb.PbList<BootstrapEndpoint> get endpoints => $_getList(1);
}

class BootstrapBundle extends $pb.GeneratedMessage {
  factory BootstrapBundle({
    $core.int? formatVersion,
    $fixnum.Int64? generatedAtUnixMs,
    $core.Iterable<BootstrapNode>? nodes,
  }) {
    final result = create();
    if (formatVersion != null) result.formatVersion = formatVersion;
    if (generatedAtUnixMs != null) result.generatedAtUnixMs = generatedAtUnixMs;
    if (nodes != null) result.nodes.addAll(nodes);
    return result;
  }

  BootstrapBundle._();

  factory BootstrapBundle.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BootstrapBundle.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BootstrapBundle',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'formatVersion',
        fieldType: $pb.PbFieldType.OU3)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'generatedAtUnixMs', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..pPM<BootstrapNode>(3, _omitFieldNames ? '' : 'nodes',
        subBuilder: BootstrapNode.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BootstrapBundle clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BootstrapBundle copyWith(void Function(BootstrapBundle) updates) =>
      super.copyWith((message) => updates(message as BootstrapBundle))
          as BootstrapBundle;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BootstrapBundle create() => BootstrapBundle._();
  @$core.override
  BootstrapBundle createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static BootstrapBundle getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BootstrapBundle>(create);
  static BootstrapBundle? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get formatVersion => $_getIZ(0);
  @$pb.TagNumber(1)
  set formatVersion($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasFormatVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearFormatVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get generatedAtUnixMs => $_getI64(1);
  @$pb.TagNumber(2)
  set generatedAtUnixMs($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasGeneratedAtUnixMs() => $_has(1);
  @$pb.TagNumber(2)
  void clearGeneratedAtUnixMs() => $_clearField(2);

  @$pb.TagNumber(3)
  $pb.PbList<BootstrapNode> get nodes => $_getList(2);
}

class BootstrapState extends $pb.GeneratedMessage {
  factory BootstrapState({
    BootstrapPhase? phase,
    $core.int? configuredCount,
    $core.int? currentlyReachableCount,
    ResultCode? lastErrorCode,
    $core.String? userMessage,
  }) {
    final result = create();
    if (phase != null) result.phase = phase;
    if (configuredCount != null) result.configuredCount = configuredCount;
    if (currentlyReachableCount != null)
      result.currentlyReachableCount = currentlyReachableCount;
    if (lastErrorCode != null) result.lastErrorCode = lastErrorCode;
    if (userMessage != null) result.userMessage = userMessage;
    return result;
  }

  BootstrapState._();

  factory BootstrapState.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BootstrapState.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BootstrapState',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aE<BootstrapPhase>(1, _omitFieldNames ? '' : 'phase',
        enumValues: BootstrapPhase.values)
    ..aI(2, _omitFieldNames ? '' : 'configuredCount',
        fieldType: $pb.PbFieldType.OU3)
    ..aI(3, _omitFieldNames ? '' : 'currentlyReachableCount',
        fieldType: $pb.PbFieldType.OU3)
    ..aE<ResultCode>(4, _omitFieldNames ? '' : 'lastErrorCode',
        enumValues: ResultCode.values)
    ..aOS(5, _omitFieldNames ? '' : 'userMessage')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BootstrapState clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BootstrapState copyWith(void Function(BootstrapState) updates) =>
      super.copyWith((message) => updates(message as BootstrapState))
          as BootstrapState;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BootstrapState create() => BootstrapState._();
  @$core.override
  BootstrapState createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static BootstrapState getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BootstrapState>(create);
  static BootstrapState? _defaultInstance;

  @$pb.TagNumber(1)
  BootstrapPhase get phase => $_getN(0);
  @$pb.TagNumber(1)
  set phase(BootstrapPhase value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasPhase() => $_has(0);
  @$pb.TagNumber(1)
  void clearPhase() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get configuredCount => $_getIZ(1);
  @$pb.TagNumber(2)
  set configuredCount($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasConfiguredCount() => $_has(1);
  @$pb.TagNumber(2)
  void clearConfiguredCount() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.int get currentlyReachableCount => $_getIZ(2);
  @$pb.TagNumber(3)
  set currentlyReachableCount($core.int value) => $_setUnsignedInt32(2, value);
  @$pb.TagNumber(3)
  $core.bool hasCurrentlyReachableCount() => $_has(2);
  @$pb.TagNumber(3)
  void clearCurrentlyReachableCount() => $_clearField(3);

  @$pb.TagNumber(4)
  ResultCode get lastErrorCode => $_getN(3);
  @$pb.TagNumber(4)
  set lastErrorCode(ResultCode value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasLastErrorCode() => $_has(3);
  @$pb.TagNumber(4)
  void clearLastErrorCode() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.String get userMessage => $_getSZ(4);
  @$pb.TagNumber(5)
  set userMessage($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasUserMessage() => $_has(4);
  @$pb.TagNumber(5)
  void clearUserMessage() => $_clearField(5);
}

class ImportBootstrapBundleRequest extends $pb.GeneratedMessage {
  factory ImportBootstrapBundleRequest({
    $core.String? code,
  }) {
    final result = create();
    if (code != null) result.code = code;
    return result;
  }

  ImportBootstrapBundleRequest._();

  factory ImportBootstrapBundleRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ImportBootstrapBundleRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ImportBootstrapBundleRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'code')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ImportBootstrapBundleRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ImportBootstrapBundleRequest copyWith(
          void Function(ImportBootstrapBundleRequest) updates) =>
      super.copyWith(
              (message) => updates(message as ImportBootstrapBundleRequest))
          as ImportBootstrapBundleRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ImportBootstrapBundleRequest create() =>
      ImportBootstrapBundleRequest._();
  @$core.override
  ImportBootstrapBundleRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ImportBootstrapBundleRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ImportBootstrapBundleRequest>(create);
  static ImportBootstrapBundleRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get code => $_getSZ(0);
  @$pb.TagNumber(1)
  set code($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasCode() => $_has(0);
  @$pb.TagNumber(1)
  void clearCode() => $_clearField(1);
}

class GenerateBootstrapBundleRequest extends $pb.GeneratedMessage {
  factory GenerateBootstrapBundleRequest({
    $core.int? preferredNodeCount,
  }) {
    final result = create();
    if (preferredNodeCount != null)
      result.preferredNodeCount = preferredNodeCount;
    return result;
  }

  GenerateBootstrapBundleRequest._();

  factory GenerateBootstrapBundleRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GenerateBootstrapBundleRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GenerateBootstrapBundleRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'preferredNodeCount',
        fieldType: $pb.PbFieldType.OU3)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GenerateBootstrapBundleRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GenerateBootstrapBundleRequest copyWith(
          void Function(GenerateBootstrapBundleRequest) updates) =>
      super.copyWith(
              (message) => updates(message as GenerateBootstrapBundleRequest))
          as GenerateBootstrapBundleRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GenerateBootstrapBundleRequest create() =>
      GenerateBootstrapBundleRequest._();
  @$core.override
  GenerateBootstrapBundleRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GenerateBootstrapBundleRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GenerateBootstrapBundleRequest>(create);
  static GenerateBootstrapBundleRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get preferredNodeCount => $_getIZ(0);
  @$pb.TagNumber(1)
  set preferredNodeCount($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPreferredNodeCount() => $_has(0);
  @$pb.TagNumber(1)
  void clearPreferredNodeCount() => $_clearField(1);
}

class GeneratedBootstrapBundle extends $pb.GeneratedMessage {
  factory GeneratedBootstrapBundle({
    $core.String? code,
    $core.int? nodeCount,
    $fixnum.Int64? generatedAtUnixMs,
  }) {
    final result = create();
    if (code != null) result.code = code;
    if (nodeCount != null) result.nodeCount = nodeCount;
    if (generatedAtUnixMs != null) result.generatedAtUnixMs = generatedAtUnixMs;
    return result;
  }

  GeneratedBootstrapBundle._();

  factory GeneratedBootstrapBundle.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GeneratedBootstrapBundle.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GeneratedBootstrapBundle',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'code')
    ..aI(2, _omitFieldNames ? '' : 'nodeCount', fieldType: $pb.PbFieldType.OU3)
    ..a<$fixnum.Int64>(
        3, _omitFieldNames ? '' : 'generatedAtUnixMs', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GeneratedBootstrapBundle clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GeneratedBootstrapBundle copyWith(
          void Function(GeneratedBootstrapBundle) updates) =>
      super.copyWith((message) => updates(message as GeneratedBootstrapBundle))
          as GeneratedBootstrapBundle;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GeneratedBootstrapBundle create() => GeneratedBootstrapBundle._();
  @$core.override
  GeneratedBootstrapBundle createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GeneratedBootstrapBundle getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GeneratedBootstrapBundle>(create);
  static GeneratedBootstrapBundle? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get code => $_getSZ(0);
  @$pb.TagNumber(1)
  set code($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasCode() => $_has(0);
  @$pb.TagNumber(1)
  void clearCode() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get nodeCount => $_getIZ(1);
  @$pb.TagNumber(2)
  set nodeCount($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasNodeCount() => $_has(1);
  @$pb.TagNumber(2)
  void clearNodeCount() => $_clearField(2);

  @$pb.TagNumber(3)
  $fixnum.Int64 get generatedAtUnixMs => $_getI64(2);
  @$pb.TagNumber(3)
  set generatedAtUnixMs($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasGeneratedAtUnixMs() => $_has(2);
  @$pb.TagNumber(3)
  void clearGeneratedAtUnixMs() => $_clearField(3);
}

class DeleteProfileRequest extends $pb.GeneratedMessage {
  factory DeleteProfileRequest({
    $core.bool? confirm,
  }) {
    final result = create();
    if (confirm != null) result.confirm = confirm;
    return result;
  }

  DeleteProfileRequest._();

  factory DeleteProfileRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteProfileRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteProfileRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'confirm')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteProfileRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteProfileRequest copyWith(void Function(DeleteProfileRequest) updates) =>
      super.copyWith((message) => updates(message as DeleteProfileRequest))
          as DeleteProfileRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteProfileRequest create() => DeleteProfileRequest._();
  @$core.override
  DeleteProfileRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteProfileRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteProfileRequest>(create);
  static DeleteProfileRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.bool get confirm => $_getBF(0);
  @$pb.TagNumber(1)
  set confirm($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasConfirm() => $_has(0);
  @$pb.TagNumber(1)
  void clearConfirm() => $_clearField(1);
}

class GetAvailableCountriesRequest extends $pb.GeneratedMessage {
  factory GetAvailableCountriesRequest({
    $core.bool? refresh,
  }) {
    final result = create();
    if (refresh != null) result.refresh = refresh;
    return result;
  }

  GetAvailableCountriesRequest._();

  factory GetAvailableCountriesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetAvailableCountriesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetAvailableCountriesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'refresh')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetAvailableCountriesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetAvailableCountriesRequest copyWith(
          void Function(GetAvailableCountriesRequest) updates) =>
      super.copyWith(
              (message) => updates(message as GetAvailableCountriesRequest))
          as GetAvailableCountriesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetAvailableCountriesRequest create() =>
      GetAvailableCountriesRequest._();
  @$core.override
  GetAvailableCountriesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetAvailableCountriesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetAvailableCountriesRequest>(create);
  static GetAvailableCountriesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.bool get refresh => $_getBF(0);
  @$pb.TagNumber(1)
  set refresh($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasRefresh() => $_has(0);
  @$pb.TagNumber(1)
  void clearRefresh() => $_clearField(1);
}

class AvailableCountries extends $pb.GeneratedMessage {
  factory AvailableCountries({
    AvailableCountriesStatus? status,
    $core.Iterable<$core.String>? countryCodes,
    $fixnum.Int64? generatedAtUnixMs,
  }) {
    final result = create();
    if (status != null) result.status = status;
    if (countryCodes != null) result.countryCodes.addAll(countryCodes);
    if (generatedAtUnixMs != null) result.generatedAtUnixMs = generatedAtUnixMs;
    return result;
  }

  AvailableCountries._();

  factory AvailableCountries.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory AvailableCountries.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'AvailableCountries',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aE<AvailableCountriesStatus>(1, _omitFieldNames ? '' : 'status',
        enumValues: AvailableCountriesStatus.values)
    ..pPS(2, _omitFieldNames ? '' : 'countryCodes')
    ..a<$fixnum.Int64>(
        3, _omitFieldNames ? '' : 'generatedAtUnixMs', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AvailableCountries clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AvailableCountries copyWith(void Function(AvailableCountries) updates) =>
      super.copyWith((message) => updates(message as AvailableCountries))
          as AvailableCountries;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static AvailableCountries create() => AvailableCountries._();
  @$core.override
  AvailableCountries createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static AvailableCountries getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<AvailableCountries>(create);
  static AvailableCountries? _defaultInstance;

  @$pb.TagNumber(1)
  AvailableCountriesStatus get status => $_getN(0);
  @$pb.TagNumber(1)
  set status(AvailableCountriesStatus value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasStatus() => $_has(0);
  @$pb.TagNumber(1)
  void clearStatus() => $_clearField(1);

  @$pb.TagNumber(2)
  $pb.PbList<$core.String> get countryCodes => $_getList(1);

  @$pb.TagNumber(3)
  $fixnum.Int64 get generatedAtUnixMs => $_getI64(2);
  @$pb.TagNumber(3)
  set generatedAtUnixMs($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasGeneratedAtUnixMs() => $_has(2);
  @$pb.TagNumber(3)
  void clearGeneratedAtUnixMs() => $_clearField(3);
}

enum Event_Payload {
  statusChanged,
  contributionChanged,
  logRecordAdded,
  notSet
}

class Event extends $pb.GeneratedMessage {
  factory Event({
    $fixnum.Int64? sequence,
    StatusChanged? statusChanged,
    ContributionChanged? contributionChanged,
    LogRecordAdded? logRecordAdded,
  }) {
    final result = create();
    if (sequence != null) result.sequence = sequence;
    if (statusChanged != null) result.statusChanged = statusChanged;
    if (contributionChanged != null)
      result.contributionChanged = contributionChanged;
    if (logRecordAdded != null) result.logRecordAdded = logRecordAdded;
    return result;
  }

  Event._();

  factory Event.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Event.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, Event_Payload> _Event_PayloadByTag = {
    10: Event_Payload.statusChanged,
    11: Event_Payload.contributionChanged,
    12: Event_Payload.logRecordAdded,
    0: Event_Payload.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Event',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..oo(0, [10, 11, 12])
    ..a<$fixnum.Int64>(
        1, _omitFieldNames ? '' : 'sequence', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOM<StatusChanged>(10, _omitFieldNames ? '' : 'statusChanged',
        subBuilder: StatusChanged.create)
    ..aOM<ContributionChanged>(11, _omitFieldNames ? '' : 'contributionChanged',
        subBuilder: ContributionChanged.create)
    ..aOM<LogRecordAdded>(12, _omitFieldNames ? '' : 'logRecordAdded',
        subBuilder: LogRecordAdded.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Event clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Event copyWith(void Function(Event) updates) =>
      super.copyWith((message) => updates(message as Event)) as Event;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Event create() => Event._();
  @$core.override
  Event createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static Event getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<Event>(create);
  static Event? _defaultInstance;

  @$pb.TagNumber(10)
  @$pb.TagNumber(11)
  @$pb.TagNumber(12)
  Event_Payload whichPayload() => _Event_PayloadByTag[$_whichOneof(0)]!;
  @$pb.TagNumber(10)
  @$pb.TagNumber(11)
  @$pb.TagNumber(12)
  void clearPayload() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  $fixnum.Int64 get sequence => $_getI64(0);
  @$pb.TagNumber(1)
  set sequence($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSequence() => $_has(0);
  @$pb.TagNumber(1)
  void clearSequence() => $_clearField(1);

  @$pb.TagNumber(10)
  StatusChanged get statusChanged => $_getN(1);
  @$pb.TagNumber(10)
  set statusChanged(StatusChanged value) => $_setField(10, value);
  @$pb.TagNumber(10)
  $core.bool hasStatusChanged() => $_has(1);
  @$pb.TagNumber(10)
  void clearStatusChanged() => $_clearField(10);
  @$pb.TagNumber(10)
  StatusChanged ensureStatusChanged() => $_ensure(1);

  @$pb.TagNumber(11)
  ContributionChanged get contributionChanged => $_getN(2);
  @$pb.TagNumber(11)
  set contributionChanged(ContributionChanged value) => $_setField(11, value);
  @$pb.TagNumber(11)
  $core.bool hasContributionChanged() => $_has(2);
  @$pb.TagNumber(11)
  void clearContributionChanged() => $_clearField(11);
  @$pb.TagNumber(11)
  ContributionChanged ensureContributionChanged() => $_ensure(2);

  @$pb.TagNumber(12)
  LogRecordAdded get logRecordAdded => $_getN(3);
  @$pb.TagNumber(12)
  set logRecordAdded(LogRecordAdded value) => $_setField(12, value);
  @$pb.TagNumber(12)
  $core.bool hasLogRecordAdded() => $_has(3);
  @$pb.TagNumber(12)
  void clearLogRecordAdded() => $_clearField(12);
  @$pb.TagNumber(12)
  LogRecordAdded ensureLogRecordAdded() => $_ensure(3);
}

class StatusChanged extends $pb.GeneratedMessage {
  factory StatusChanged({
    Status? status,
  }) {
    final result = create();
    if (status != null) result.status = status;
    return result;
  }

  StatusChanged._();

  factory StatusChanged.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory StatusChanged.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'StatusChanged',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aOM<Status>(1, _omitFieldNames ? '' : 'status', subBuilder: Status.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  StatusChanged clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  StatusChanged copyWith(void Function(StatusChanged) updates) =>
      super.copyWith((message) => updates(message as StatusChanged))
          as StatusChanged;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static StatusChanged create() => StatusChanged._();
  @$core.override
  StatusChanged createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static StatusChanged getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<StatusChanged>(create);
  static StatusChanged? _defaultInstance;

  @$pb.TagNumber(1)
  Status get status => $_getN(0);
  @$pb.TagNumber(1)
  set status(Status value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasStatus() => $_has(0);
  @$pb.TagNumber(1)
  void clearStatus() => $_clearField(1);
  @$pb.TagNumber(1)
  Status ensureStatus() => $_ensure(0);
}

class ContributionChanged extends $pb.GeneratedMessage {
  factory ContributionChanged({
    Contribution? contribution,
  }) {
    final result = create();
    if (contribution != null) result.contribution = contribution;
    return result;
  }

  ContributionChanged._();

  factory ContributionChanged.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ContributionChanged.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ContributionChanged',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..aOM<Contribution>(1, _omitFieldNames ? '' : 'contribution',
        subBuilder: Contribution.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ContributionChanged clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ContributionChanged copyWith(void Function(ContributionChanged) updates) =>
      super.copyWith((message) => updates(message as ContributionChanged))
          as ContributionChanged;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ContributionChanged create() => ContributionChanged._();
  @$core.override
  ContributionChanged createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ContributionChanged getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ContributionChanged>(create);
  static ContributionChanged? _defaultInstance;

  @$pb.TagNumber(1)
  Contribution get contribution => $_getN(0);
  @$pb.TagNumber(1)
  set contribution(Contribution value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasContribution() => $_has(0);
  @$pb.TagNumber(1)
  void clearContribution() => $_clearField(1);
  @$pb.TagNumber(1)
  Contribution ensureContribution() => $_ensure(0);
}

class LogRecordAdded extends $pb.GeneratedMessage {
  factory LogRecordAdded({
    $core.List<$core.int>? daemonIncarnation,
    LogRecord? record,
  }) {
    final result = create();
    if (daemonIncarnation != null) result.daemonIncarnation = daemonIncarnation;
    if (record != null) result.record = record;
    return result;
  }

  LogRecordAdded._();

  factory LogRecordAdded.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory LogRecordAdded.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'LogRecordAdded',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'hydra.local.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'daemonIncarnation', $pb.PbFieldType.OY)
    ..aOM<LogRecord>(2, _omitFieldNames ? '' : 'record',
        subBuilder: LogRecord.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LogRecordAdded clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LogRecordAdded copyWith(void Function(LogRecordAdded) updates) =>
      super.copyWith((message) => updates(message as LogRecordAdded))
          as LogRecordAdded;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static LogRecordAdded create() => LogRecordAdded._();
  @$core.override
  LogRecordAdded createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static LogRecordAdded getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<LogRecordAdded>(create);
  static LogRecordAdded? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get daemonIncarnation => $_getN(0);
  @$pb.TagNumber(1)
  set daemonIncarnation($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasDaemonIncarnation() => $_has(0);
  @$pb.TagNumber(1)
  void clearDaemonIncarnation() => $_clearField(1);

  @$pb.TagNumber(2)
  LogRecord get record => $_getN(1);
  @$pb.TagNumber(2)
  set record(LogRecord value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasRecord() => $_has(1);
  @$pb.TagNumber(2)
  void clearRecord() => $_clearField(2);
  @$pb.TagNumber(2)
  LogRecord ensureRecord() => $_ensure(1);
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names');
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names');
