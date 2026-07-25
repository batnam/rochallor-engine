# Giám sát trạng thái Quy trình và Quyết định (Monitoring)
- Bản đồ nhiệt quy trình (Process Heatmaps): Hiển thị trực quan luồng quy trình trên sơ đồ BPMN. Tô màu các hoạt động (Activities) dựa trên tần suất dữ liệu đi qua, giúp nhận diện ngay lập tức các điểm nghẽn (Bottlenecks) trong hệ thống.
- Giám sát Thực thể Quy trình (Process Instance Monitoring): Theo dõi danh sách tất cả các instance đang chạy, đã hoàn thành hoặc bị lỗi. Xem chi tiết trạng thái của từng token (Token Position) đang dừng ở bước nào trên sơ đồ.
- Giám sát Bảng Quyết định (DMN/DRD Monitoring): Xem lịch sử thực thi của các quy trình quyết định dựa trên luật (Decision Tables). Cockpit cho phép kiểm tra chi tiết các giá trị đầu vào (Inputs) đã kích hoạt quy tắc nào để cho ra kết quả đầu ra (Outputs).

# Quản lý lỗi và Sự cố (Incident Management)
- Phát hiện và cảnh báo sự cố (Incident Tracking): Tự động hiển thị các lỗi kỹ thuật phát sinh trong quá trình vận hành (ví dụ: lỗi kết nối bên thứ ba, lỗi code từ External Task Worker, lỗi script...).
- Xem Stack Trace lỗi: Cho phép kiểm tra chi tiết thông tin log lỗi (Stack Trace) trực tiếp trên giao diện mà không cần truy cập vào server hay file log hệ thống.
- Xử lý lại các tác vụ lỗi (Retry Jobs): Cho phép người vận hành tăng số lần thử lại (Increment Retries) cho một tác vụ lỗi cụ thể hoặc áp dụng đồng loạt (Batch) sau khi sự cố hệ thống đã được khắc phục.

# Quản lý Biến và Dữ liệu quy trình (Variable Manipulation)
- Xem và kiểm tra dữ liệu biến: Giám sát toàn bộ các biến (Variables) đi kèm với một Process Instance tại thời điểm hiện tại hoặc trong lịch sử (đối với bản Enterprise).
- Sửa đổi giá trị biến trực tiếp: Người quản trị có quyền sửa đổi giá trị (Value) hoặc kiểu dữ liệu (Type) của một biến ngay khi quy trình đang chạy để điều hướng luồng đi hoặc sửa lỗi dữ liệu.

# Can thiệp và Điều hướng Luồng thực thi (Process Modification)
- Dịch chuyển Token (Process Instance Modification): Tính năng can thiệp sâu cho phép "hủy bỏ" một bước đang chạy và "kích hoạt" một bước khác bất kỳ trên sơ đồ. Rất hữu ích khi cần đưa một quy trình quay lại bước trước đó hoặc bỏ qua một bước bị lỗi.
- Tạm dừng/Tiếp tục (Suspend/Activate): Hỗ trợ tạm dừng (Suspend) một Process Instance cụ thể hoặc toàn bộ một Process Definition (phiên bản quy trình) để bảo trì, sau đó kích hoạt lại (Activate) bình thường.

