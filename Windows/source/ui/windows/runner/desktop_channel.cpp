// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

#include "desktop_channel.h"

#include <shellapi.h>
#include <strsafe.h>
#include <string>
#include <vector>

namespace {
constexpr wchar_t kRunKey[] = L"Software\\Microsoft\\Windows\\CurrentVersion\\Run";
constexpr wchar_t kRunValue[] = L"IRVIO";

bool ExecutableCommand(std::wstring& command) {
  std::vector<wchar_t> buffer(32768);
  const DWORD length = GetModuleFileNameW(nullptr, buffer.data(), static_cast<DWORD>(buffer.size()));
  if (!length || length >= buffer.size()) return false;
  command = L"\"" + std::wstring(buffer.data(), length) + L"\"";
  return command.size() <= 260;
}

LONG ReadAutostart(std::wstring& value) {
  DWORD bytes = 0;
  LONG status = RegGetValueW(HKEY_CURRENT_USER, kRunKey, kRunValue, RRF_RT_REG_SZ,
                            nullptr, nullptr, &bytes);
  if (status != ERROR_SUCCESS) return status;
  if (bytes > 32768 || bytes < sizeof(wchar_t) || bytes % sizeof(wchar_t)) return ERROR_INVALID_DATA;
  std::vector<wchar_t> buffer(bytes / sizeof(wchar_t));
  status = RegGetValueW(HKEY_CURRENT_USER, kRunKey, kRunValue, RRF_RT_REG_SZ,
                       nullptr, buffer.data(), &bytes);
  if (status != ERROR_SUCCESS) return status;
  if (bytes < sizeof(wchar_t) || buffer[bytes / sizeof(wchar_t) - 1] != L'\0') return ERROR_INVALID_DATA;
  value.assign(buffer.data(), bytes / sizeof(wchar_t) - 1);
  return ERROR_SUCCESS;
}

std::wstring ToWide(const std::string& value) {
  if (value.empty() || value.size() > 4096) return {};
  const int length = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
                                        static_cast<int>(value.size()), nullptr, 0);
  if (!length) return {};
  std::wstring wide(length, L'\0');
  if (!MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
                           static_cast<int>(value.size()), wide.data(), length)) return {};
  return wide;
}

void Autostart(const flutter::MethodCall<flutter::EncodableValue>& call,
               std::unique_ptr<flutter::MethodResult<flutter::EncodableValue>> result) {
  std::wstring expected, current;
  if (!ExecutableCommand(expected)) {
    result->Error("autostart_path", "The executable path exceeds Windows Run-key limits.");
    return;
  }
  LONG status = ReadAutostart(current);
  const bool missing = status == ERROR_FILE_NOT_FOUND || status == ERROR_PATH_NOT_FOUND;
  if (!missing && status != ERROR_SUCCESS) {
    result->Error("autostart_read", "Cannot read the current-user startup registration.");
    return;
  }
  if (!missing && current != expected) {
    result->Error("autostart_conflict", "Refusing to replace another IRVIO startup registration.");
    return;
  }
  if (call.method_name() == "autostartGet") {
    result->Success(flutter::EncodableValue(!missing));
    return;
  }
  const bool* enabled = call.arguments() ? std::get_if<bool>(call.arguments()) : nullptr;
  if (!enabled) { result->Error("invalid_argument", "Expected an autostart boolean."); return; }
  if (*enabled) {
    HKEY key = nullptr;
    status = RegCreateKeyExW(HKEY_CURRENT_USER, kRunKey, 0, nullptr, 0, KEY_SET_VALUE,
                            nullptr, &key, nullptr);
    if (status == ERROR_SUCCESS) {
      status = RegSetValueExW(key, kRunValue, 0, REG_SZ,
                             reinterpret_cast<const BYTE*>(expected.c_str()),
                             static_cast<DWORD>((expected.size() + 1) * sizeof(wchar_t)));
      RegCloseKey(key);
    }
  } else if (!missing) {
    status = RegDeleteKeyValueW(HKEY_CURRENT_USER, kRunKey, kRunValue);
  } else { status = ERROR_SUCCESS; }
  if (status != ERROR_SUCCESS) {
    result->Error("autostart_write", "Cannot update the current-user startup registration.");
    return;
  }
  result->Success();
}

bool Notify(HWND window, const flutter::EncodableValue* arguments) {
  const auto* map = arguments ? std::get_if<flutter::EncodableMap>(arguments) : nullptr;
  if (!map) return false;
  const auto title = map->find(flutter::EncodableValue("title"));
  const auto body = map->find(flutter::EncodableValue("body"));
  if (title == map->end() || body == map->end()) return false;
  const auto* title_text = std::get_if<std::string>(&title->second);
  const auto* body_text = std::get_if<std::string>(&body->second);
  if (!title_text || !body_text) return false;
  const auto heading = ToWide(*title_text), content = ToWide(*body_text);
  if (heading.empty() || content.empty()) return false;
  NOTIFYICONDATAW icon{};
  icon.cbSize = sizeof(icon);
  icon.hWnd = window;
  icon.uID = 1;  // Pinned tray_manager 0.5.3 owns this icon on the top-level window.
  icon.uFlags = NIF_INFO | NIF_REALTIME;
  icon.dwInfoFlags = NIIF_INFO | NIIF_RESPECT_QUIET_TIME;
  StringCchCopyNW(icon.szInfoTitle, ARRAYSIZE(icon.szInfoTitle), heading.c_str(), ARRAYSIZE(icon.szInfoTitle)-1);
  StringCchCopyNW(icon.szInfo, ARRAYSIZE(icon.szInfo), content.c_str(), ARRAYSIZE(icon.szInfo)-1);
  return Shell_NotifyIconW(NIM_MODIFY, &icon) != FALSE;
}
}  // namespace

std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>>
CreateDesktopChannel(flutter::BinaryMessenger* messenger, HWND window) {
  auto channel = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
      messenger, "irvio/windows_desktop", &flutter::StandardMethodCodec::GetInstance());
  channel->SetMethodCallHandler([window](const auto& call, auto result) {
    if (call.method_name() == "autostartGet" || call.method_name() == "autostartSet") {
      Autostart(call, std::move(result));
    } else if (call.method_name() == "notify") {
      result->Success(flutter::EncodableValue(Notify(window, call.arguments())));
    } else { result->NotImplemented(); }
  });
  return channel;
}
