# Codex Subscription Router

> Tài liệu tiếng Anh: [README.en.md](README.en.md)

Codex Subscription Router cho phép dùng nhiều gói ChatGPT/Codex hợp lệ trong
một ứng dụng Router riêng. Tài khoản đăng nhập ban đầu là **tài khoản chính
(`Primary`)**: Router luôn dùng tài khoản này trước. Những tài khoản thêm vào
được tách riêng và chỉ được dùng khi Primary hết hạn mức hoặc không thể xử lý
thêm cuộc hội thoại.

> [!IMPORTANT]
> Router tạo một bản sao độc lập của ứng dụng chính thức. Nó **không sửa, thay
> thế hoặc tắt** ChatGPT/Codex bản Microsoft Store hay ứng dụng ChatGPT chính
> thức trên macOS.

> [!WARNING]
> Đây là dự án không chính thức, phụ thuộc vào phiên bản ứng dụng gốc và không
> do OpenAI hỗ trợ. Hãy đọc mã nguồn, tuân thủ điều khoản của từng tài khoản và
> không dùng Router để vượt quyền truy cập hoặc giới hạn sử dụng.

![Menu quản lý nhiều tài khoản](screenshots/account-menu.png)

## Bắt đầu nhanh trên Windows

### Cách cài hiện tại

Windows hiện có bộ cài bằng **nhấp đúp từ thư mục mã nguồn đã clone**, chưa phải
file `Setup.exe` tự chứa mọi thứ. Sau khi đã có mã nguồn và đủ điều kiện bên dưới,
anh chỉ cần mở thư mục bằng Explorer rồi nhấp đúp:

```text
Install Codex Subscription Router.cmd
```

Ứng dụng cài đặt sẽ tự tạo shortcut **Codex Subscription Router** trên Desktop và mở
Router sau khi cài xong. Anh không cần gõ lệnh để thêm tài khoản hoặc mở Router
hằng ngày.

### Điều kiện cần có

- Windows x64;
- ChatGPT/Codex từ Microsoft Store đã được cài;
- Python 3;
- Go 1.26 trở lên;
- Node.js 22.12 trở lên và npm;
- thư mục mã nguồn của repository này.

Nếu chưa có mã nguồn, chạy một lần:

```powershell
git clone https://github.com/LightHaru/codex-subscription-router.git
cd codex-subscription-router
```

Sau đó mở thư mục vừa clone trong Explorer và nhấp đúp
`Install Codex Subscription Router.cmd`.

> Nếu thiếu `node_modules`, bộ cài tự chạy `npm ci --ignore-scripts` để lấy
> công cụ build đã khóa phiên bản. Bộ cài không tự cài Python, Go hoặc Node,
> và cũng không tự bỏ qua phiên bản ChatGPT/Codex chưa được kiểm tra tương thích.

### Bộ cài làm gì?

1. Tìm đúng ứng dụng ChatGPT/Codex đã cài từ Microsoft Store.
2. Tạo một bản Router tạm, kiểm tra xong mới thay bản Router cũ.
3. Chỉ đóng các tiến trình nằm trong thư mục Router riêng; không đóng theo tên
   chung `ChatGPT.exe`, vì vậy không đụng vào ứng dụng Microsoft Store gốc.
4. Chuyển bản Router cũ vào thư mục backup có thể khôi phục.
5. Tạo hoặc sửa shortcut Desktop và mở bản Router độc lập.

Các đường dẫn chính:

| Đường dẫn | Dùng để làm gì |
| --- | --- |
| `%LOCALAPPDATA%\Codex Subscription Router\app` | Bản ứng dụng Router độc lập và `routerctl.exe` |
| `%LOCALAPPDATA%\Codex Subscription Router\Codex Subscription Router.cmd` | File mở Router với profile riêng |
| Desktop | Shortcut `Codex Subscription Router.lnk` |
| `%APPDATA%\Codex Subscription Router` | Hồ sơ desktop riêng của Router |
| `%USERPROFILE%\.codex-mux` | Trạng thái Router, dữ liệu tài khoản phụ và backup |

Hướng dẫn kỹ thuật chi tiết hơn nằm ở [docs/WINDOWS.md](docs/WINDOWS.md).

## Dùng Router hằng ngày

### Tài khoản chính và tài khoản phụ

- **Primary** là tài khoản đã đăng nhập trước trên Codex/ChatGPT của anh. Nó là
  tài khoản chính, là thông tin hiển thị mặc định của app và luôn được dùng cho
  cuộc hội thoại mới khi còn hạn mức.
- Mỗi tài khoản thêm vào có thư mục dữ liệu Codex và thông tin xác thực riêng.
- Khi Primary hết hạn mức hoặc không thể xử lý, Router mới chọn một tài khoản
  phụ còn dùng được.
- Router giữ nguyên tài khoản đang xử lý một cuộc hội thoại để các lượt tiếp
  theo vẫn có đủ ngữ cảnh.
- Nếu tài khoản đang xử lý hết hạn mức, Router chuyển cuộc hội thoại sang tài
  khoản phụ phù hợp, tiếp tục cuộc hội thoại và lưu tài khoản mới đang xử lý.

Nói ngắn gọn: tạo `New chat` mới thì Primary được ưu tiên; đang chat mà Primary
hết hạn mức thì Router cố chuyển sang tài khoản phụ còn hạn mức thay vì để app
dừng.

### Thêm tài khoản phụ trên Windows

1. Mở **Codex Subscription Router** từ shortcut Desktop.
2. Mở menu tài khoản ở cuối thanh bên.
3. Bấm **Add another subscription**.
4. Router mở quy trình đăng nhập chính thức trong trình duyệt. Nếu trình duyệt
   không tự mở, bấm **Continue to ChatGPT**.
5. Đăng nhập trên trang chính thức rồi quay lại Router.
6. Khi xong, hộp thoại tự đóng và hiện thông báo kết nối thành công.

Router Windows không hiển thị device code và không hỏi mật khẩu. Nó chỉ chấp
nhận URL HTTPS thuộc `chatgpt.com` hoặc `auth.openai.com`; việc đăng nhập và
lưu thông tin xác thực do tiến trình Codex chính chủ xử lý.

### Hủy đăng nhập đang chờ

Nếu không muốn thêm tài khoản nữa, bấm **Cancel sign-in** trong hộp thoại. Router
sẽ hủy quy trình đăng nhập và xóa đúng tài khoản phụ chưa kết nối, vì vậy không
còn dòng **Waiting for sign-in** bị sót lại.

Nếu đăng nhập vừa hoàn tất ngay trước lúc bấm hủy, Router giữ lại tài khoản đã
kết nối để tránh xóa nhầm. Không thể dùng thao tác này để xóa Primary.

> Giao diện macOS hiện vẫn dùng device code cho đến khi phần giao diện đó được
> chuyển đổi riêng.

### Khi tất cả tài khoản đều hết hạn mức

Nếu không còn tài khoản nào có hạn mức, Router hiển thị một thông báo tổng hợp
về hạn mức và lần reset tiếp theo đã biết. Nó không lặp lại vô ích yêu cầu trên
tài khoản đã cạn hạn mức.

## Profile, Plugins và reset hạn mức

![Bộ chọn tài khoản trong Plugins](screenshots/plugin-account-picker-secondary-final.png)

### Mục `Profile`

Mở `Settings` (hoặc `Cài đặt`) → `Profile`.

Ban đầu, `Profile` hiển thị số liệu tổng hợp của các tài khoản đã kết nối với
các avatar xếp chồng lên nhau. Bấm vào avatar của một tài khoản để xem thông
tin và số liệu riêng của tài khoản đó. Bấm lại avatar đã chọn để quay về chế độ
tổng hợp.

### Mục `Plugins`

Mở `Settings` (hoặc `Cài đặt`) → `Plugins`.

Ở đầu trang có bộ chọn tài khoản. Cấu hình plugin và MCP được dùng chung, còn
`Apps`, trạng thái kết nối và thao tác đăng nhập OAuth sẽ sử dụng tài khoản
đang chọn.

### Bảng reset hạn mức

Mở bảng `Usage remaining` hoặc bảng reset hạn mức có sẵn trong app. Bộ chọn tài
khoản trong bảng này đổi số dư hiển thị theo tài khoản đã chọn. Khi dùng một
lượt reset, lượt đó chỉ bị trừ ở đúng tài khoản đã chọn.

## An toàn dữ liệu

| Vị trí | Nội dung |
| --- | --- |
| `~/.codex` | Thông tin, cuộc hội thoại và bộ nhớ đệm của Primary |
| `~/.codex-mux/state.json` | Metadata tài khoản và tài khoản đang xử lý từng cuộc hội thoại |
| `~/.codex-mux/accounts/<id>/codex-home` | Thư mục dữ liệu Codex riêng của từng tài khoản phụ |
| `~/.codex-mux/control-token` | Token ngẫu nhiên cho dịch vụ điều khiển cục bộ |
| `~/.codex-mux/backups` | Bản Router cũ để khôi phục khi cần |
| `%APPDATA%\Codex Subscription Router` | Hồ sơ riêng của Router trên Windows |

- Dịch vụ điều khiển chỉ lắng nghe tại `127.0.0.1` và dùng token ngẫu nhiên
  256-bit cho các API riêng.
- Router không trả mã OAuth qua API và không cố ý ghi mã này vào log.
- Repository không có endpoint telemetry của Router và không phân phối binary
  đã patch của OpenAI.
- Cấu hình plugin/MCP được đồng bộ từ Primary. Vì vậy dữ liệu bí mật ghi trực tiếp bên
  trong cấu hình MCP dùng chung có thể xuất hiện trong thư mục tài khoản phụ;
  các thư mục này không phải ranh giới bí mật tuyệt đối cho cấu hình dùng chung.

Đọc [SECURITY.md](SECURITY.md) và
[docs/SECURITY-MODEL.md](docs/SECURITY-MODEL.md) trước khi báo lỗi bảo mật.

## Khi ChatGPT/Codex chính thức cập nhật

Không ghi đè trực tiếp lên bản Router đang hoạt động. Trước tiên, xem
[docs/COMPATIBILITY.md](docs/COMPATIBILITY.md). Nếu phiên bản ứng dụng gốc đã
được kiểm tra, chạy lại bộ cài:

| Nền tảng | Cách tạo lại Router |
| --- | --- |
| Windows | Nhấp đúp `Install Codex Subscription Router.cmd` hoặc chạy `python scripts/patch_windows.py --force --launch` |
| macOS | Chạy `./install.sh` hoặc `python3 scripts/patch_app.py --force` |

Router sẽ dừng an toàn nếu phát hiện phiên bản gốc hoặc giao diện chưa được
kiểm tra, thay vì cài dở dang. Hãy giữ bản backup cho đến khi bản mới chạy ổn.

## Cài trên macOS Apple silicon

Trên macOS, Router tạo ứng dụng độc lập tại:

- `~/Applications/Codex Subscription Router.app`
- `~/Applications/Codex Subscription Router Computer Use.app`

Máy cần có ứng dụng ChatGPT chính thức ở `/Applications/ChatGPT.app`, Python
3, Go 1.26+, Node.js 22.12+/npm, Xcode Command Line Tools và certificate ký
ứng dụng Apple phù hợp.

```sh
git clone https://github.com/LightHaru/codex-subscription-router.git
cd codex-subscription-router
./install.sh
```

Giữ cùng Apple signing team giữa các lần tạo lại Router để không mất quyền
Privacy đã cấp. Bản ký ad-hoc chỉ dùng để chẩn đoán; `Computer Use` và
`Appshots` có thể không hoạt động đầy đủ với kiểu ký này.

Khi cần, cấp quyền cho Router độc lập — không phải ChatGPT chính thức — trong
`System Settings → Privacy & Security`:

| Quyền | Ứng dụng |
| --- | --- |
| `Accessibility` | Codex Subscription Router |
| `Screen & System Audio Recording` | Codex Subscription Router Computer Use |

## Phát triển và kiểm tra

```sh
npm ci --ignore-scripts
npm run check
npm run release:check
```

Trên Windows, có thể chạy riêng kiểm tra bộ cài/công cụ patch:

```powershell
python scripts/test_patch_windows.py
```

Bộ kiểm tra gồm Go test/vet, kiểm tra JavaScript và UI, công cụ patch Python,
bộ cài Windows, các điểm tương thích giao diện và thông tin phát hành. Xem
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) nếu cần chi tiết kỹ thuật về bộ
điều phối nhiều tài khoản.

## Giới hạn hiện tại

- Đây là công cụ chỉ phát hành mã nguồn. Chưa có Windows `Setup.exe` độc lập,
  chưa đóng gói sẵn Node/Go/Python runtime và không phân phối lại binary
  ChatGPT/Codex.
- Bản Windows là bản port thử nghiệm, chỉ hoạt động với các phiên bản ứng dụng
  gốc đã được kiểm tra trong `docs/COMPATIBILITY.md`.
- Bản cập nhật từ ChatGPT/Codex có thể yêu cầu review tương thích và cập nhật
  phần patch trước khi cài lại.
- `Computer Use` và `Appshots` vẫn là tính năng riêng của macOS.
- Lần lấy lịch sử tổng hợp ban đầu giới hạn 500 cuộc hội thoại cho mỗi tài
  khoản.
- Một tài khoản chỉ được Router chọn khi hợp lệ, bật, đã kết nối và còn hạn
  mức.

## Ghi công, giấy phép và đóng góp

Dự án gốc cùng thông báo bản quyền được ghi công cho
[Bennett Blackham (b-nnett)](https://github.com/b-nnett/codex-subscription-router).
Fork LightHaru giữ nguyên giấy phép MIT và các thông báo cần thiết, đồng thời
bổ sung phần Windows, bộ cài, định tuyến ưu tiên Primary và tài liệu liên
quan.

- Đọc [NOTICE.md](NOTICE.md) để xem đầy đủ ghi công và giới hạn phân phối
  OpenAI binaries.
- Mã nguồn dùng [MIT License](LICENSE). ChatGPT, Codex và các ứng dụng desktop
  chính thức là sản phẩm của OpenAI, không nằm trong giấy phép của repository
  này.
- Đọc [CONTRIBUTING.md](CONTRIBUTING.md) trước khi đóng góp.
- Làm theo [SECURITY.md](SECURITY.md) khi báo lỗi liên quan credential, signing
  hoặc dịch vụ cục bộ.
