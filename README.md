# Codex Relay

> Tài liệu tiếng Anh: [README.en.md](README.en.md)

Codex Relay cho phép dùng nhiều gói ChatGPT/Codex hợp lệ trong
một ứng dụng Router riêng. Khi cài lần đầu, tài khoản đang có trong Codex gốc
được chọn làm **Primary** mặc định. Sau đó anh có thể đổi Primary ngay trong
Router; lựa chọn này được lưu riêng và không chạy theo tài khoản đang chọn ở
Codex gốc. Router luôn ưu tiên Primary, rồi mới chuyển sang tài khoản phụ khi
Primary hết hạn mức hoặc không thể xử lý thêm cuộc hội thoại.

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
Install Codex Relay.cmd
```

Ứng dụng cài đặt sẽ tự tạo shortcut **Codex Relay** trên Desktop và mở
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
git clone https://github.com/LightHaru/codex-relay.git
cd codex-relay
```

Sau đó mở thư mục vừa clone trong Explorer và nhấp đúp
`Install Codex Relay.cmd`.

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
| `%LOCALAPPDATA%\Codex Relay\app` | Bản ứng dụng Relay độc lập và `routerctl.exe` |
| `%LOCALAPPDATA%\Codex Relay\Codex Relay.cmd` | File mở Relay với profile riêng |
| Desktop | Shortcut `Codex Relay.lnk` |
| `%APPDATA%\Codex Relay` | Hồ sơ desktop riêng của bản cài mới |
| `%USERPROFILE%\.codex-mux` | Trạng thái Router, dữ liệu tài khoản phụ và backup |
| `%LOCALAPPDATA%\Codex Relay Updater\router-updater.exe` | Trình cập nhật dùng cho nút **Cập nhật ngay** trong Relay |

### Nếu anh đang dùng bản tên cũ `Codex Subscription Router`

Không cần đăng nhập lại. Khi cài hoặc cập nhật lên `v0.3.0`, Relay tạo bản mới
ở `%LOCALAPPDATA%\Codex Relay\app`, chỉ dừng đúng tiến trình nằm trong thư mục
Router cũ, rồi chuyển bản cũ vào `%USERPROFILE%\.codex-mux\backups\...`.
Tài khoản, quota, chat đã gán, dữ liệu các tài khoản phụ và profile Electron
được giữ nguyên. Trong lần chuyển đổi đầu, Relay tiếp tục dùng profile cũ nếu
profile mới chưa tồn tại; đây là chủ ý để không đánh mất lịch sử/thiết lập
desktop. Sau khi xác nhận Relay chạy ổn, anh có thể xóa shortcut hoặc thư mục
bản cũ trong backup theo cách thủ công nếu muốn dọn dung lượng.

Hướng dẫn kỹ thuật chi tiết hơn nằm ở [docs/WINDOWS.md](docs/WINDOWS.md).

### Cập nhật Router ngay trong ứng dụng

Sau lần cài đầu tiên, anh không cần gõ lệnh để nâng cấp Router. Mỗi bản phát
hành có một tệp thông báo nhỏ trên GitHub và một gói mã nguồn kèm mã băm
SHA-256. Khi có phiên bản Router mới, trong cửa sổ Router sẽ hiện thông báo
**Cập nhật ngay**.

Khi anh bấm nút đó, Router sẽ tự động:

1. tải đúng gói phát hành từ GitHub;
2. kiểm tra mã băm và kiểm tra đường dẫn bên trong gói để tránh gói lỗi;
3. chờ Router hiện tại đóng, chỉ dừng các tiến trình của Router riêng;
4. chạy lại bộ cài với bản ChatGPT/Codex chính thức đang có trên máy;
5. giữ nguyên tài khoản, lịch sử chat, quyền sở hữu chat, hồ sơ desktop và
   shortcut Desktop, rồi mở bản Router mới.

Trình cập nhật nằm ngoài thư mục ứng dụng Router để có thể thay thế bản đang
chạy. Nó không sửa ứng dụng ChatGPT/Codex từ Microsoft Store, không nhận mật
khẩu hay token OAuth, và không gửi token điều khiển cục bộ. Nếu chưa có bản
phát hành mới hoặc máy không có tệp thông báo, Router sẽ không hiện banner.

Đây là cơ chế cập nhật **gói mã nguồn**, chưa phải file `Setup.exe` độc lập.
Lần cài đầu vẫn cần Python 3, Go, Node.js/npm và ứng dụng chính thức từ
Microsoft Store. Xem [hướng dẫn Windows đầy đủ](docs/WINDOWS.md#in-app-updates)
để biết cách xử lý khi cập nhật lỗi.

### Nếu gặp “Unable to send message — Update Agent sandbox”

Đây là lỗi thiết lập sandbox Windows, không phải lỗi quota của tài khoản. Một số
bản Codex Windows bị kẹt khi cấu hình đặt `[windows] sandbox = "elevated"`, nên
cả chat mới và chat cũ đều không gửi được lượt tiếp theo.

Router Windows tự dùng chế độ `unelevated` cho các tiến trình Router và các tài
khoản phụ. Cách này không sửa, không xóa và không đăng xuất cấu hình gốc trong
`%USERPROFILE%\.codex`; vì vậy Codex Microsoft Store vẫn giữ nguyên thiết lập
riêng của nó. Sau khi nâng cấp, chỉ cần đóng rồi mở lại shortcut **Codex
Relay** một lần. Nếu vẫn còn hộp thoại cũ, chạy lại bộ cài từ
checkout để Router thay wrapper mới, rồi mở lại Router.

## Dùng Router hằng ngày

### Tài khoản chính và tài khoản phụ

- **Primary** là tài khoản chính do Router chọn. Lần đầu nó thường là tài khoản
  đã đăng nhập sẵn trên Codex gốc, nhưng sau đó không còn bị ràng buộc với Codex
  gốc.
- Mỗi tài khoản thêm vào có thư mục dữ liệu Codex và thông tin xác thực riêng.
- Khi Primary hết hạn mức hoặc không thể xử lý, Router mới chọn một tài khoản
  phụ còn dùng được.
- Router giữ nguyên tài khoản đang xử lý một cuộc hội thoại để các lượt tiếp
  theo vẫn có đủ ngữ cảnh.
- Nếu tài khoản đang xử lý hết hạn mức, Router chuyển cuộc hội thoại sang tài
  khoản phụ phù hợp. Router sao chép bản history cục bộ của chat vào kho Codex
  riêng của tài khoản phụ, tiếp tục đúng chat đó và lưu tài khoản mới đang xử
  lý. History gốc không bị sửa.

Nói ngắn gọn: tạo `New chat` mới thì Primary được ưu tiên; đang chat mà Primary
hết hạn mức thì Router cố chuyển sang tài khoản phụ còn hạn mức thay vì để app
dừng.

### Chọn Primary và quản lý tài khoản

1. Mở menu tài khoản ở cuối thanh bên của **Codex Relay**.
2. Bấm **Account settings**.
3. Bấm **Set as Primary** ở tài khoản đã kết nối để đổi tài khoản ưu tiên. Việc
   này lưu Primary riêng của Router, sau đó Router tự khởi động lại các phiên
   Codex con của chính Router để thay đổi có hiệu lực ngay. Tài khoản và cửa
   sổ Codex gốc không bị đăng xuất hoặc khởi động lại.
4. Muốn gỡ tài khoản phụ, bấm **Remove** rồi xác nhận. Router không cho xóa
   Primary hiện tại; hãy chọn tài khoản khác làm Primary trước. Nếu tài khoản
   đang giữ chat, hộp xác nhận sẽ nói rõ số chat bị ảnh hưởng và chỉ xóa liên kết
   định tuyến, không xóa file history gốc.

Mục **Usage remaining** cộng phần quota đã đọc được từ các tài khoản. Nếu một
tài khoản chưa trả quota, giao diện vẫn hiển thị tổng phần đã biết và ghi
**Updating quota…** hoặc **Quota unavailable**; nó không biến dữ liệu thiếu thành
`0%` hay hiện dấu `–` gây hiểu nhầm.

Mỗi dòng tài khoản hiển thị tên hồ sơ ChatGPT (`display name`, rồi đến username
hoặc email khi cần), gói đang dùng và nhãn Router. Dòng quota của chính tài
khoản đó cũng hiển thị thời gian hồi của từng cửa sổ hạn mức, ví dụ `Reset 5h:
1h 20m`. Di chuột lên dòng để xem mốc giờ đầy đủ theo máy anh. Nếu ChatGPT chưa
trả thời gian reset, Router ghi rõ là chưa có dữ liệu thay vì tự đoán.

### Dùng chat cũ với Router

Anh có thể tiếp tục **chat cũ** bằng Router, không chỉ chat mới. Mở chat đó từ
thanh bên của **Codex Relay** rồi gửi tin nhắn tiếp theo tại cửa
sổ Router. Nếu tài khoản đang sở hữu chat cũ đã hết hạn mức, Router sẽ sao chép
history cục bộ cần thiết sang kho tách biệt của tài khoản còn quota, sau đó tiếp
tục đúng cuộc hội thoại bằng tài khoản này.

Router không thể chặn một lượt đã gửi từ cửa sổ **Codex gốc** vì app gốc không
kết nối với Router. Anh không cần tắt Codex gốc, nhưng hãy mở lại chính chat đó
trong **Codex Relay** trước khi gửi lượt muốn Relay chuyển quota.

### Thêm tài khoản phụ trên Windows

1. Mở **Codex Relay** từ shortcut Desktop.
2. Mở menu tài khoản ở cuối thanh bên.
3. Bấm **Add another subscription**.
4. Router mở một cửa sổ đăng nhập riêng nằm trong chính ứng dụng Router; không
   gọi Chrome, Edge hoặc trình duyệt mặc định của anh.
5. Đăng nhập trên trang ChatGPT chính thức trong cửa sổ đó. Mỗi lần bấm đăng
   nhập, Router tạo một phiên trình duyệt tạm mới, không mang cookie hoặc dữ
   liệu web của lần đăng nhập trước.
6. Nếu lỡ đóng cửa sổ đăng nhập, bấm **Open secure sign-in** để mở lại một
   cửa sổ riêng mới, hoặc bấm **Cancel sign-in** để hủy tài khoản đang chờ.
7. Khi xong, cửa sổ đăng nhập và hộp xác nhận tự đóng, sau đó Router hiện thông
   báo kết nối thành công.

Router Windows không hiển thị device code và không hỏi mật khẩu. Nó chỉ chấp
nhận URL HTTPS thuộc `chatgpt.com` hoặc `auth.openai.com`; việc đăng nhập và
lưu thông tin xác thực do tiến trình Codex chính chủ xử lý. Cửa sổ đăng nhập
không có quyền Node, không có preload của Router, không dùng cookie của app
chính và tự xóa dữ liệu phiên khi đóng.

> Lưu ý: Router có thể bảo đảm cookie, local storage và cache của cửa sổ này
> là mới. Nó không và không nên xóa đăng nhập một lần (SSO), passkey hoặc nhận
> dạng do Windows, Google, Microsoft hay Apple quản lý. Nếu trang chính thức
> tự chọn một tài khoản từ SSO, hãy chọn **Use another account** ngay trên trang
> đó.

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
| `%APPDATA%\Codex Relay` | Hồ sơ riêng của bản cài Relay mới trên Windows |

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

Nếu chỉ có bản Router mới, hãy dùng nút **Cập nhật ngay** trong ứng dụng; Router
sẽ tự tải, kiểm tra, khởi động lại và mở lại. Khi chính ChatGPT/Codex chính
thức cập nhật, không ghi đè trực tiếp lên bản Router đang hoạt động. Trước
tiên, xem [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md). Hiện Router đã giữ
hồ sơ cho cả gói cũ `26.810.7004.0` và gói mới `26.818.2441.0`. Nếu phiên bản
ứng dụng gốc đã được kiểm tra, chạy lại bộ cài:

| Nền tảng | Cách tạo lại Router |
| --- | --- |
| Windows | Nhấp đúp `Install Codex Relay.cmd` hoặc chạy `python scripts/patch_windows.py --force --launch` |
| macOS | Chạy `./install.sh` hoặc `python3 scripts/patch_app.py --force` |

Router sẽ dừng an toàn nếu phát hiện phiên bản gốc hoặc giao diện chưa được
kiểm tra, thay vì cài dở dang. Hãy giữ bản backup cho đến khi bản mới chạy ổn.

## Cài trên macOS Apple silicon

Trên macOS, Router tạo ứng dụng độc lập tại:

- `~/Applications/Codex Relay.app`
- `~/Applications/Codex Relay Computer Use.app`

Máy cần có ứng dụng ChatGPT chính thức ở `/Applications/ChatGPT.app`, Python
3, Go 1.26+, Node.js 22.12+/npm, Xcode Command Line Tools và certificate ký
ứng dụng Apple phù hợp.

```sh
git clone https://github.com/LightHaru/codex-relay.git
cd codex-relay
./install.sh
```

Giữ cùng Apple signing team giữa các lần tạo lại Router để không mất quyền
Privacy đã cấp. Bản ký ad-hoc chỉ dùng để chẩn đoán; `Computer Use` và
`Appshots` có thể không hoạt động đầy đủ với kiểu ký này.

Khi cần, cấp quyền cho Router độc lập — không phải ChatGPT chính thức — trong
`System Settings → Privacy & Security`:

| Quyền | Ứng dụng |
| --- | --- |
| `Accessibility` | Codex Relay |
| `Screen & System Audio Recording` | Codex Relay Computer Use |

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
  phần patch trước khi cài lại; bản chưa có hồ sơ sẽ dừng an toàn.
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
