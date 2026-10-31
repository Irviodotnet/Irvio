// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

#ifndef IRVIO_SINGLE_INSTANCE_H_
#define IRVIO_SINGLE_INSTANCE_H_
#include <windows.h>
#include <string>

constexpr wchar_t kIrvioWindowClass[] = L"IRVIO_MAIN_WINDOW";
constexpr UINT kIrvioActivateWindow = WM_APP + 71;

class DesktopInstance {
 public:
  DesktopInstance() = default;
  ~DesktopInstance();
  DesktopInstance(const DesktopInstance&) = delete;
  DesktopInstance& operator=(const DesktopInstance&) = delete;
  bool Acquire();
  bool IsFirst() const { return first_; }
  bool ActivateExisting();
 private:
  HANDLE mutex_ = nullptr;
  bool first_ = false;
  std::wstring user_sid_;
};
#endif
