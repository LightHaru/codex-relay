# Codex Subscription Router

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> **English.** Run several eligible ChatGPT/Codex subscriptions from one
> independently patched desktop copy. The initial controller account remains
> the **Primary** identity and is used first; extra subscriptions are isolated
> and used only when Primary cannot accept more work.
>
> **Tiếng Việt.** Dùng nhiều gói ChatGPT/Codex hợp lệ trong một bản desktop
> được tạo độc lập. Tài khoản điều khiển ban đầu vẫn là **Primary / tài khoản
> chính** và luôn được ưu tiên; các tài khoản thêm vào được tách riêng, chỉ
> được dùng khi Primary không còn nhận được lượt làm việc.

This repository is maintained at
[LightHaru/codex-subscription-router](https://github.com/LightHaru/codex-subscription-router).
It contains source and build tooling only: it does **not** ship OpenAI/ChatGPT
binaries or modify the official installed package.

> [!WARNING]
> **English.** This is an unofficial, version-sensitive project. It is not
> affiliated with or supported by OpenAI. Review the source, keep every
> connected subscription compliant with its governing terms, and do not use it
> to bypass access controls or rate limits.
>
> **Tiếng Việt.** Đây là dự án không chính thức và phụ thuộc sát vào phiên bản
> ứng dụng gốc; không do OpenAI hỗ trợ. Hãy đọc mã nguồn, tuân thủ điều khoản
> của từng gói tài khoản và không dùng công cụ để vượt quyền truy cập hoặc giới
> hạn sử dụng.

![Multi-subscription account menu](screenshots/account-menu.png)

## What it does / Chức năng chính

| English | Tiếng Việt |
| --- | --- |
| **Primary-first routing.** New chats use the controller/Primary account while it has usable capacity. | **Ưu tiên Primary.** Chat mới đi qua tài khoản chính khi còn quota khả dụng. |
| **Isolated secondary subscriptions.** Each extra account has a separate Codex home and credentials. | **Tách biệt tài khoản phụ.** Mỗi tài khoản thêm có Codex home và thông tin đăng nhập riêng. |
| **Sticky thread ownership.** Follow-up turns stay with the assigned account, preserving the conversation context. | **Giữ chủ sở hữu cuộc hội thoại.** Các lượt tiếp theo bám theo tài khoản đã nhận thread để giữ ngữ cảnh. |
| **Quota-aware failover.** When an owner is depleted or unavailable, the Router resumes the thread on an eligible secondary account. | **Chuyển dự phòng theo quota.** Khi tài khoản đang sở hữu thread cạn quota hoặc không khả dụng, Router tiếp tục thread ở tài khoản phụ còn dùng được. |
| **In-app account management on Windows.** Add, inspect, and cancel pending secondary sign-ins from the profile menu. | **Quản lý tài khoản ngay trong Windows app.** Thêm, xem và hủy đăng nhập phụ đang chờ từ menu hồ sơ. |
| **Account-aware Profile, Plugins, and resets.** View identity/statistics per account and scope supported settings surfaces to the selected subscription. | **Profile, Plugins và reset theo tài khoản.** Xem thống kê từng gói và chọn đúng gói cho các bề mặt cài đặt được hỗ trợ. |

## Routing model / Cách Router chọn tài khoản

The Router replaces the copied app's bundled Codex executable with a small Go
multiplexer. The original executable remains beside it as a preserved
`codex.real` binary. One desktop connection is then routed to one official
Codex child per connected subscription.

> Router thay executable Codex trong **bản copy** bằng một multiplexer Go nhỏ.
> Executable gốc vẫn được giữ dưới tên `codex.real`. Một kết nối desktop được
> phân luồng đến một Codex child chính chủ cho từng subscription đã kết nối.

```text
Independent Router copy
        │
        │ one desktop/app-server connection
        ▼
    codex-mux
    ├── Primary / controller → default Codex home
    ├── Subscription 2       → isolated Codex home
    └── Subscription N       → isolated Codex home
                              │
                              └── persistent thread ID → account owner
```

### Primary-first policy / Chính sách ưu tiên Primary

1. **New chat / Chat mới:** Primary is selected whenever its short and longer
   usage windows both have capacity.
2. **Fallback / Dự phòng:** only after Primary is unavailable or depleted does
   the Router rank eligible secondary accounts using quota pressure, reset
   availability, pinned-thread count, and stable ordering.
3. **Follow-up / Lượt tiếp theo:** the thread returns to its persisted owner;
   it is not moved merely to balance load.
4. **Failover / Chuyển khi cạn quota:** the Router reads and resumes the
   thread on an eligible account, persists the new owner, and forwards the
   turn there.
5. **All depleted / Tất cả đã cạn:** the app returns one combined quota alert
   with the next known reset instead of repeatedly retrying an exhausted
   account.

Primary is the controller account initialized from the default Codex profile.
It remains the visible app identity and the default routing choice; adding
secondary subscriptions does not turn them into the controller.

> Primary là tài khoản điều khiển được khởi tạo từ hồ sơ Codex mặc định. Nó vẫn
> là identity hiển thị của app và là lựa chọn định tuyến mặc định; thêm
> subscription phụ không làm thay đổi tài khoản điều khiển.

Read [Architecture](docs/ARCHITECTURE.md) for protocol-level detail.

## Platform status and compatibility / Nền tảng và tương thích

| Platform | Current path | Verified upstream input |
| --- | --- | --- |
| **macOS, Apple silicon** | Existing signed independent-app workflow | ChatGPT `26.803.61601`, bundle build `6396` |
| **Windows x64 (preview)** | Local-checkout, double-click installer | Microsoft Store package `OpenAI.Codex_26.810.7004.0_x64__2p2nqsd0c76g0` |

The patchers are intentionally fail-closed: they verify the official bundle
version/hash plus exact renderer and binary anchors before activation. An
unreviewed app update stops the build rather than applying a partial rewrite.
See [Compatibility](docs/COMPATIBILITY.md) for the exact hashes and currently
reviewed versions.

> Patcher được thiết kế **fail-closed**: kiểm tra phiên bản/hash của bundle gốc
> cùng các anchor renderer/binary chính xác trước khi kích hoạt. Nếu ứng dụng
> gốc vừa cập nhật mà chưa được review, quá trình build sẽ dừng thay vì patch
> dở dang. Xem [Compatibility](docs/COMPATIBILITY.md) để biết hash và phiên
> bản đã kiểm tra.

## Windows x64: one-click local installer / Cài Windows bằng nhấp đúp

### What “one-click” means / “Một lần nhấp” có nghĩa là gì

The Windows installer is a **double-click installer from this source
checkout**, not a standalone `Setup.exe` and not a redistributed copy of the
official Store app. After the checkout is present, double-click:

`Install Codex Subscription Router.cmd`

It will:

1. Locate the installed Store package through `powershell.exe
   Get-AppxPackage` (the Store app does not need to be closed).
2. Build and verify a staged copy before touching the active Router.
3. Stop only executables below
   `%LOCALAPPDATA%\Codex Subscription Router` — never processes selected merely
   by the name `ChatGPT.exe`, so the Microsoft Store app is not targeted.
4. Move the previous stable Router copy to a recoverable
   `%USERPROFILE%\.codex-mux\backups\...` directory.
5. Create or repair a direct Desktop shortcut and launch the independent
   Router.

> Sau khi đã có source checkout, chỉ cần nhấp đúp `Install Codex Subscription
> Router.cmd`. Installer tự tìm package Store, build bản copy trong staging,
> chỉ đóng các tiến trình nằm dưới thư mục Router, backup bản Router cũ, tạo
> shortcut Desktop trực tiếp và mở Router mới. Nó **không** sửa, thay thế hay
> đóng ChatGPT/Codex bản Microsoft Store.

### Windows prerequisites / Điều kiện Windows

- Windows 11 x64;
- Microsoft Store ChatGPT/Codex package already installed;
- Python 3;
- Go 1.26 or newer;
- Node.js 22.12+ and npm;
- this source checkout.

If `node_modules` is absent, the installer obtains the locked ASAR build tool
with `npm ci --ignore-scripts`. It does not automatically install Python, Go,
or Node, and it deliberately does not bypass an unreviewed Store hash.

> Nếu thiếu `node_modules`, installer sẽ chạy `npm ci --ignore-scripts` để lấy
> công cụ build đã khóa phiên bản. Nó không tự cài Python/Go/Node và không tự
> bỏ qua hash Store chưa được review.

### Get the checkout / Lấy source checkout

```powershell
git clone https://github.com/LightHaru/codex-subscription-router.git
cd codex-subscription-router
```

Then open the folder in Explorer and double-click
`Install Codex Subscription Router.cmd`. A terminal command is not needed for
the actual install once the checkout exists.

For development or automation, the equivalent command is:

```powershell
py -3 scripts/patch_windows.py --force --launch
```

The Windows installer uses these stable paths:

| Path | Purpose |
| --- | --- |
| `%LOCALAPPDATA%\Codex Subscription Router\app` | Independent copied app and `routerctl.exe` |
| `%LOCALAPPDATA%\Codex Subscription Router\Codex Subscription Router.cmd` | Launcher with a dedicated Electron profile |
| Windows Desktop known folder | Direct `Codex Subscription Router.lnk` shortcut |
| `%APPDATA%\Codex Subscription Router` | Dedicated Electron desktop profile |
| `%USERPROFILE%\.codex-mux` | Router state, account homes, token, and recoverable backups |

For the complete Windows behavior and troubleshooting notes, see
[Windows installation](docs/WINDOWS.md).

## macOS Apple silicon / Cài đặt macOS Apple silicon

The existing macOS workflow creates an independently signed app at:

- `~/Applications/Codex Subscription Router.app`
- `~/Applications/Codex Subscription Router Computer Use.app`

Requirements are the official ChatGPT app in `/Applications/ChatGPT.app`,
Xcode Command Line Tools, Python 3, Go 1.26+, Node.js 22.12+/npm, and an Apple
Development or Developer ID Application signing identity.

Use the current repository checkout so the installer builds from the
LightHaru source you reviewed:

```sh
git clone https://github.com/LightHaru/codex-subscription-router.git
cd codex-subscription-router
./install.sh
```

The script installs locked build dependencies, stops only a prior independent
Router bundle, creates a recoverable backup, builds/signs the app, and launches
it. For manual control:

```sh
npm ci --ignore-scripts
python3 scripts/patch_app.py
open "$HOME/Applications/Codex Subscription Router.app"
```

Reuse the same Apple signing team for rebuilds. Changing teams can invalidate
existing privacy grants; the patcher rejects an unexpected change unless an
explicit diagnostic override is supplied. Ad-hoc signing is diagnostic only,
and Appshots/Computer Use can be unavailable under it.

> Trên macOS Apple silicon, hãy clone source LightHaru rồi chạy `./install.sh`.
> Bản app độc lập sẽ nằm trong `~/Applications`. Cần giữ cùng Apple signing team
> giữa các lần rebuild để không mất các quyền Privacy đã cấp.

### macOS permissions / Quyền trên macOS

Grant the independent Router — not the official ChatGPT app — the following
permissions in **System Settings → Privacy & Security** when needed:

| Permission | Independent application |
| --- | --- |
| Accessibility | Codex Subscription Router |
| Screen & System Audio Recording | Codex Subscription Router Computer Use |

See [SMOKE-TEST.md](docs/SMOKE-TEST.md) for the signed-app verification flow.

## Add a subscription / Thêm subscription

### Windows browser sign-in / Đăng nhập trình duyệt trên Windows

1. Open the profile menu at the bottom of the Router sidebar.
2. Select **Add another subscription**.
3. The Router asks the official Codex child app-server to begin the supported
   ChatGPT browser sign-in. It can open the verified page automatically; use
   **Continue to ChatGPT** if the browser was blocked.
4. Complete sign-in in the browser, then return to the Router. The dialog
   closes automatically and a success toast confirms the connected
   subscription.

The Windows Router displays **no device code**, does not collect a password,
and accepts only HTTPS `chatgpt.com` or `auth.openai.com` authorization URLs.
The official child owns the callback and credential storage.

> Windows không hiển thị device code và không hỏi mật khẩu. Router chỉ mở URL
> HTTPS thuộc `chatgpt.com` hoặc `auth.openai.com` do Codex child chính chủ trả
> về; callback và credential vẫn thuộc child chính chủ.

### Cancel an unfinished sign-in / Hủy đăng nhập đang chờ

Choose **Cancel sign-in** in the dialog or on a pending row. The Router
cancels the official child flow, removes only the unconnected **secondary**
account and its isolated local home, then refreshes the menu.

If browser completion wins the cancellation race, the already connected account
is preserved rather than deleted. Primary cannot be cancelled through this
flow.

> Bấm **Cancel sign-in** sẽ hủy flow chính chủ và xóa chỉ tài khoản phụ chưa
> kết nối cùng thư mục tách biệt của nó. Nếu browser vừa hoàn tất trước khi hủy,
> tài khoản đã kết nối được giữ lại; Primary không thể bị xóa bởi thao tác này.

The current macOS UI retains its existing device-code sign-in experience until
its UI is migrated separately.

## Profiles, Plugins, and resets / Profile, Plugins và reset

![Account-scoped plugin connections](screenshots/plugin-account-picker-secondary-final.png)

### Profile statistics / Thống kê Profile

Profile statistics start in a combined view with overlapping account photos.
Select an account photo to inspect only that subscription's identity and
statistics; select it again to return to the combined view.

> Profile bắt đầu ở chế độ tổng hợp với avatar chồng lên nhau. Chọn avatar để
> xem identity/thống kê của riêng subscription đó; chọn lần nữa để quay lại
> tổng hợp.

### Settings → Plugins / Cài đặt → Plugins

The Plugins page provides a subscription picker. Plugin definitions and managed
MCP configuration are shared, while Apps, connection status, and OAuth login
operations are scoped to the selected subscription.

> Plugin definition và managed MCP config được dùng chung; nhưng Apps, trạng
> thái kết nối và OAuth login sẽ theo subscription đang chọn.

### Native rate-limit resets / Reset quota native

The native rate-limit sheet includes an account picker. Selecting a
subscription changes the displayed usage/reset balance and ensures a consumed
reset applies only to that account.

> Sheet reset quota native có bộ chọn account. Khi đổi subscription, số dư hiển
> thị và reset được dùng đều áp dụng đúng cho account đó.

## Local data and safety / Dữ liệu cục bộ và an toàn

| Location | Purpose |
| --- | --- |
| `~/.codex` | Primary Codex credentials, conversations, and cache |
| `~/.codex-mux/state.json` | Account metadata and persisted thread ownership |
| `~/.codex-mux/accounts/<id>/codex-home` | Isolated secondary account homes |
| `~/.codex-mux/control-token` | Random token for the loopback-only control service |
| `~/.codex-mux/backups` | Recoverable Router app backups |
| `~/Library/Application Support/Codex Subscription Router` | Independent macOS desktop profile |
| `%APPDATA%\Codex Subscription Router` | Independent Windows desktop profile |

- The control service binds only to `127.0.0.1` and uses a random 256-bit
  token for private routes.
- OAuth material stays in the relevant account home. It is not returned by the
  control API or intentionally logged by the Router.
- The project has no Router telemetry endpoint and does not distribute
  patched OpenAI application binaries.
- Plugin/MCP definitions are intentionally synchronized from Primary. Inline
  secrets inside shared MCP configuration can therefore be copied to isolated
  account homes; account isolation is **not** a separate secret boundary for
  such shared definitions.

> Service điều khiển chỉ bind `127.0.0.1` và bảo vệ route riêng bằng token ngẫu
> nhiên 256-bit. OAuth token nằm trong Codex home của từng account; Router không
> trả token qua API hoặc cố ý ghi log. Tuy nhiên, secret inline trong MCP config
> dùng chung có thể được đồng bộ sang account phụ — vì vậy các account home
> không phải là ranh giới bí mật độc lập cho cấu hình shared.

Read [SECURITY.md](SECURITY.md) and
[Security model](docs/SECURITY-MODEL.md) before reporting a credential,
signing, or local control-service issue.

## Update or rebuild / Cập nhật hoặc build lại

When the official app updates, do **not** overwrite the independent Router
copy. First check [Compatibility](docs/COMPATIBILITY.md); then rebuild from
the reviewed source:

| Platform | Rebuild action |
| --- | --- |
| Windows | Double-click `Install Codex Subscription Router.cmd` again, or run `py -3 scripts/patch_windows.py --force --launch` |
| macOS | Run `./install.sh` from the checkout, or `python3 scripts/patch_app.py --force` |

Unknown official bundles and changed renderer anchors fail closed. Preserve the
backup until the rebuilt Router has passed a smoke test.

> Khi app gốc cập nhật, không ghi đè bản Router độc lập. Kiểm tra Compatibility
> trước, rồi chạy lại installer/patcher từ source đã review. Giữ bản backup cho
> tới khi Router mới chạy smoke test thành công.

## Development and verification / Phát triển và kiểm tra

```sh
npm ci --ignore-scripts
npm run check
npm run release:check
```

Windows-focused renderer/installer checks can also be run directly:

```powershell
python scripts/test_patch_windows.py
```

The verification suite includes Go tests, vetting, JavaScript syntax/UI tests,
Python patcher tests, exact renderer-anchor checks, and release metadata
checks. The optional current-renderer fixture is enabled only when
`CODEX_MUX_WINDOWS_RENDERER_DIR` points to a reviewed unpacked Store renderer.

> Bộ kiểm tra bao gồm Go test/vet, JS syntax/UI test, Python patcher test,
> anchor renderer chính xác và kiểm tra metadata release. Fixture renderer
> Windows thật là tùy chọn và chỉ chạy khi
> `CODEX_MUX_WINDOWS_RENDERER_DIR` trỏ tới renderer Store đã review.

## Limits and non-goals / Giới hạn và ngoài phạm vi

- This is source-only tooling. There is currently **no standalone Windows
  `Setup.exe`**, no bundled Node/Go/Python runtime, and no redistributed
  ChatGPT/Codex binary.
- Upstream app updates can require a deliberate compatibility review and new
  anchors.
- Windows is a version-pinned preview port; macOS Computer Use/Appshots remain
  macOS-specific.
- The initial merged history fetch is limited to 500 threads per account.
- Combined “skills explored” totals can count a skill once per account because
  upstream profile responses expose counts rather than global skill IDs.
- An account must be valid, enabled, connected, and have capacity before the
  Router can select it.

> **Tiếng Việt.** Đây là source-only tooling: hiện chưa có Windows `Setup.exe`
> độc lập, chưa đóng gói Node/Go/Python runtime và không phân phối lại binary
> ChatGPT/Codex. Các bản cập nhật ứng dụng gốc có thể cần review compatibility
> và anchor mới. Windows là bản port preview khóa theo phiên bản; Computer
> Use/Appshots vẫn chỉ dành cho macOS. Một account chỉ được Router chọn khi hợp
> lệ, bật, đã kết nối và còn capacity.

## Attribution / Ghi công tác giả gốc

The original project and its copyright notice are credited to **Bennett
Blackham (b-nnett)**. This LightHaru-maintained repository retains the original
MIT license and notices; contributions should preserve applicable attribution
and license text.

> Dự án gốc và thông báo bản quyền được ghi công cho **Bennett Blackham
> (b-nnett)**. Bản duy trì trên LightHaru giữ nguyên giấy phép MIT và các thông
> báo cần thiết; đóng góp mới cần tôn trọng attribution và license hiện có.

## Contributing, security, and license / Đóng góp, bảo mật và giấy phép

- Read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting changes.
- Follow [SECURITY.md](SECURITY.md) for credential, signing, or local-service
  reports.
- Releases follow the source-only process in
  [RELEASING.md](docs/RELEASING.md).
- Source is available under the [MIT License](LICENSE). ChatGPT, Codex, and
  the official desktop applications are OpenAI products and are not licensed
  by this repository.

> **Tiếng Việt.** Đọc [CONTRIBUTING.md](CONTRIBUTING.md) trước khi đóng góp và
> [SECURITY.md](SECURITY.md) khi báo lỗi nhạy cảm. Release chỉ phát hành source
> theo [RELEASING.md](docs/RELEASING.md). Mã nguồn dùng [MIT License](LICENSE);
> ChatGPT, Codex và ứng dụng desktop chính thức là sản phẩm của OpenAI, không
> nằm trong giấy phép của repository này.
