# Codex Relay

> Tài liệu tiếng Anh: [README.en.md](README.en.md)

Codex Relay là Router độc lập cho Codex trên Windows. Ở chế độ **Unified Pool Gateway**, Codex chỉ nhìn thấy một Relay API, một Relay identity, một task authority và một pool quota. Các tài khoản ChatGPT/Codex thêm vào Relay chỉ là credential source ẩn phía sau pool; chúng không trở thành chat, thread hay worker công khai.

> [!IMPORTANT]
> Relay không phải sản phẩm của OpenAI và không biến nhiều gói thành một gói thanh toán mới. `500%` chỉ là tổng dung lượng định tuyến đã xác nhận của năm nguồn 100%; upstream vẫn ghi usage vào tài khoản thật đang được dùng.

> [!WARNING]
> Chỉ kết nối tài khoản bạn được phép sử dụng và tuân thủ điều khoản dịch vụ. Không dùng Relay để vượt quyền truy cập, giới hạn hoặc cơ chế an toàn. Không đưa token, `auth.json` hay nội dung chat vào issue, log hoặc pull request.

## Mô hình

```text
Codex desktop
     │  một API / một identity / một session
     ▼
Relay Unified Pool Gateway
     │  một logical task authority
      ▼
PoolQuotaLedger + QuotaAwarePoolScheduler
     ├── credential source A (ẩn)
     ├── credential source B (ẩn)
     ├── credential source C (ẩn)
     └── credential source D (ẩn)
```

Mỗi request đi qua cùng một Relay API và cùng một task authority. Scheduler đọc cả
hai cửa sổ quota (5 giờ và tuần), bỏ qua source đã hết quota, rồi phân phối fair-share
giữa các source còn đủ điều kiện bằng cursor bền vững. Nếu upstream vẫn từ chối một
source, Relay đánh dấu source đó `DEPLETED` và thử lại đúng body, session, thread,
turn và connection trên source kế tiếp trong **cùng logical request**. Không tạo worker,
task, thread hoặc sự kiện “move chat” mới khi đổi credential ẩn.

### Cam kết và ranh giới an toàn

- Một `RelayGatewayWorker` logic sở hữu thread, session, Goal, tool/approval, canonical history và output stream.
- Mỗi logical turn có đúng một `PoolLease`; pool revision và source transition được ghi nguyên tử, idempotent và có heartbeat.
- Retry A→B→C→D chỉ được thực hiện trước visible output hoặc side effect. Body, model, thread và Goal không đổi.
- Lỗi mạng, HTTP 502/503/504 hoặc SSE kết thúc trước `response.completed` được xem là lỗi transport, không phải hết quota. Trước output, Gateway tự xoay source; source lỗi chỉ bị `suspect/cooldown` tạm thời và vẫn giữ nguyên credential/quota.
- Khi app hoặc máy tính khởi động lại, lease cũ chưa phát output/tool được giải phóng trước khi Gateway nhận request. Cùng request ID có thể tiếp tục mà không mắc `409`; các request trùng đồng thời cùng chờ một upstream flight thay vì chạy hai lần.
- Nếu quota bị cắt sau output, command, file hoặc tool side effect, Relay không replay. Turn thành `recovery-required`, source bị loại ở lượt sau và kết quả phải được review. Không quảng cáo đây là continuation của stream đã phát.
- Nếu upstream im lặng hoặc đóng sau `response.output_item.done` mà chưa có terminal, Relay chờ một khoảng rất ngắn cho `response.completed`; nếu vẫn thiếu, nó gửi terminal phục hồi chuẩn và mux hiển thị lý do Relay rõ ràng thay vì chuỗi `stream closed before response.completed`.
- Khi mọi source cạn, Relay trả một lỗi cấp pool. Timeout, lỗi mạng và lỗi model không tự bị coi là quota hết.

## Cài nhanh Windows

Sau khi một release chính thức có manifest, PowerShell cài bằng một lệnh:

```powershell
irm https://github.com/LightHaru/codex-relay/releases/latest/download/install-codex-relay.ps1 | iex
```

Bootstrap kiểm tra host, manifest và SHA-256 trước khi cài; không tự cài Python/Go/Node và không sửa Codex gốc.

Từ mã nguồn:

```powershell
git clone https://github.com/LightHaru/codex-relay.git
cd codex-relay
```

Mở thư mục bằng Explorer và nhấp đúp `Install Codex Relay.cmd`. Bộ cài tạo shortcut **Codex Relay**, profile Electron riêng và `codex-home` riêng.

Điều kiện build: Windows x64, Codex/ChatGPT Microsoft Store, Python 3, Go 1.26+, Node.js 22.12+ và npm. Relay không đọc tài khoản trong `%USERPROFILE%\\.codex` của Codex gốc.

| Đường dẫn | Mục đích |
| --- | --- |
| `%LOCALAPPDATA%\\Codex Relay\\app` | App Relay độc lập và `codex.real.exe` |
| `%APPDATA%\\Codex Relay\\codex-home` | Home Relay host riêng (chỉ làm task authority khi được chọn) |
| `%USERPROFILE%\\.codex-mux` | Pool ledger, source homes, canonical history, backup |
| `%LOCALAPPDATA%\\Codex Relay Updater` | Updater nằm ngoài thư mục app |

Codex gốc vẫn giữ profile, credential, history và process riêng.

## Đăng nhập và sử dụng

Mở **Codex Relay**, vào menu tài khoản và chọn **Add another subscription**. Browser login do app-server nguồn tương ứng thực hiện; Relay chỉ giữ pending state và quan sát hoàn tất. Mỗi source có home và `auth.json` riêng; không copy `auth.json` giữa Codex gốc và Relay.

Trong Settings → **Usage & billing**, Relay giữ nguyên trang con và menu gốc, rồi thêm đúng một panel trong cột nội dung. Panel có tổng quan **Codex Relay Pool** và một thẻ cho từng tài khoản đã kết nối: quota ngắn/dài, thời gian reset, plan, credits, reset credits và lỗi cụ thể nếu nguồn đó không đọc được. Đây là thông tin billing trong trang cài đặt; task route và public status vẫn không lộ danh tính nguồn.

Tên/ảnh đang hiện ở menu là **Relay authority** (ví dụ Agent Aira), tức identity công khai của một worker duy nhất; nó không phải bằng chứng rằng mọi request đang bị khóa vào tài khoản đó. Khi authority hết quota, Gateway đánh dấu source đó cạn và gửi lại đúng request qua source kế tiếp trong pool, còn thread, session, Goal và UI vẫn giữ nguyên một mạch. Nguồn thực tế chỉ dùng cho routing ẩn và được đối chiếu trong panel Usage & billing.

Thông báo task cũng chỉ ở cấp pool, như “Relay Pool tiếp tục phiên làm việc” hoặc “Relay Pool đã hết quota khả dụng”. Không có “Move chat to Subscription 2”, tên worker hay account owner công khai.

## API cục bộ

Control API chỉ bind `127.0.0.1` và cần token cài đặt. Gateway model API là `POST /v1/responses`, cũng cần bearer token loopback; credential header được tạo lại từ source được chọn và cookie/token nguồn khác không được chuyển tiếp.

Ở contract v2, `GET /v1/router/status` và `GET /v1/thread-route` chỉ trả identity `relay`/`Codex Relay Pool`, pool aggregate và recovery state cần thiết. Metadata source chi tiết chỉ dành cho endpoint quản lý tài khoản có token.

## Tương thích và cập nhật

Patcher dùng anchor của Store bundle đã review và fail closed với bundle chưa biết. Router core dùng `wire_api = responses` với custom provider cục bộ. Sau khi Codex gốc update, chạy compatibility gate trước khi cài; không nới anchor để ép build.

Nếu bundle/app-server chưa có profile đã review, Relay vẫn giữ một API/identity
duy nhất nhưng chuyển sang safe mode và tạm tắt credential failover cho đến khi
profile đó được kiểm thử.

Updater là executable riêng bên ngoài app. Khi release có `windows-update.json`, nút **Update now** tải source archive, kiểm tra SHA-256/path, chờ đúng process Relay đóng, cài bản mới và mở lại. Pool state, canonical history, shortcut và credential homes được giữ nguyên; updater không có quyền đóng/sửa Codex gốc.

## Kiểm thử

```powershell
npm ci --ignore-scripts
npm run check
npm run release:check
git diff --check
```

Suite bao phủ state v3 migration/rollback, quota-aware fair-share pool ledger, CAS, heartbeat/crash recovery, retry cùng body, early-stream failover, late-stream no-replay, sanitization, UI Usage & billing in-flow và app-server `codex.real.exe` thật với upstream giả lập. E2E này chứng minh một authority và source rotation A→B→C→D trong cùng session, không phải quota live.

E2E tài khoản thật phải chạy riêng với prompt ngắn, tài khoản được ủy quyền và report đã khử định danh. Ghi transition quan sát được, lease/pool revision, canonical hash/size, Goal continuity, duplicate/lost output, exit code cuối và việc Codex gốc vẫn mở. Không có live evidence thì ghi `LIVE PENDING`, không ghi `PASS`.

Xem [docs/TEST-MATRIX.md](docs/TEST-MATRIX.md), [docs/SMOKE-TEST.md](docs/SMOKE-TEST.md), [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md) và [docs/SECURITY-MODEL.md](docs/SECURITY-MODEL.md).

## Đóng góp và giấy phép

Đây là dự án cộng đồng. Issue chỉ nên chứa log đã khử token/PII, bản Store, bước tái hiện và exit code; không đính kèm `app.asar`, executable, `auth.json` hay history người dùng.

MIT License — xem [LICENSE](LICENSE). Codex Relay kế thừa ý tưởng và mã nguồn ban đầu của [LightHaru/codex-subscription-router](https://github.com/LightHaru/codex-subscription-router); xin cảm ơn tác giả gốc và các contributor trước đây. Dự án không liên kết, không được OpenAI chứng thực và không phân phối binary của OpenAI.
