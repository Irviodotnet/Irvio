// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

#include "single_instance.h"
#include <sddl.h>
#include <string>
#include <vector>

namespace {
bool WindowBelongsToUser(HWND window, const std::wstring& expected, DWORD& pid) {
  GetWindowThreadProcessId(window, &pid);
  HANDLE process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, pid);
  if (!process) return false;
  HANDLE token = nullptr;
  const bool opened = OpenProcessToken(process, TOKEN_QUERY, &token) != FALSE;
  CloseHandle(process);
  if (!opened) return false;
  DWORD size = 0;
  GetTokenInformation(token, TokenUser, nullptr, 0, &size);
  std::vector<BYTE> buffer(size);
  const bool read = GetTokenInformation(token, TokenUser, buffer.data(), size, &size) != FALSE;
  CloseHandle(token);
  if (!read) return false;
  LPWSTR sid = nullptr;
  if (!ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(buffer.data())->User.Sid, &sid)) return false;
  const bool matches = expected == sid;
  LocalFree(sid);
  return matches;
}
}  // namespace

DesktopInstance::~DesktopInstance() { if (mutex_) CloseHandle(mutex_); }

bool DesktopInstance::Acquire() {
  HANDLE token = nullptr;
  if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token)) return false;
  DWORD size = 0;
  GetTokenInformation(token, TokenUser, nullptr, 0, &size);
  std::vector<BYTE> buffer(size);
  const bool read = GetTokenInformation(token, TokenUser, buffer.data(), size, &size) != FALSE;
  CloseHandle(token);
  if (!read) return false;
  LPWSTR sid = nullptr;
  if (!ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(buffer.data())->User.Sid, &sid)) return false;
  user_sid_ = sid;
  const std::wstring name = L"Local\\IRVIO.UI." + user_sid_;
  const std::wstring acl = L"D:P(A;;GA;;;SY)(A;;GA;;;" + std::wstring(sid) + L")";
  LocalFree(sid);
  PSECURITY_DESCRIPTOR descriptor = nullptr;
  if (!ConvertStringSecurityDescriptorToSecurityDescriptorW(acl.c_str(), SDDL_REVISION_1, &descriptor, nullptr)) return false;
  SECURITY_ATTRIBUTES attributes{sizeof(SECURITY_ATTRIBUTES), descriptor, FALSE};
  mutex_ = CreateMutexW(&attributes, FALSE, name.c_str());
  const DWORD error = GetLastError();
  LocalFree(descriptor);
  first_ = mutex_ && error != ERROR_ALREADY_EXISTS;
  return mutex_ != nullptr;
}

bool DesktopInstance::ActivateExisting() {
  for (int i = 0; i < 50; ++i) {
    for (HWND window = FindWindowExW(nullptr, nullptr, kIrvioWindowClass, nullptr);
         window; window = FindWindowExW(nullptr, window, kIrvioWindowClass, nullptr)) {
      DWORD process = 0;
      if (!WindowBelongsToUser(window, user_sid_, process)) continue;
      AllowSetForegroundWindow(process);
      return PostMessageW(window, kIrvioActivateWindow, 0, 0) != FALSE;
    }
    Sleep(100);
  }
  return false;
}
