import {
  Modal,
} from "@arco-design/web-vue";

/**
 * 打开 Humbert 统一确认对话框。
 *
 * 过去部分页面直接调用 Wails 的 Dialogs.Question。那类窗口由操作系统绘制，
 * 在 macOS 上会出现系统蓝色按钮、大号信息图标和与 Humbert 完全不同的圆角，
 * 因而即使业务逻辑正确，视觉上也会像突然跳出了另一个应用。
 *
 * 这里统一改用已经接入 Humbert 主题变量的 Arco Modal：
 *
 * - 普通确认使用墨蓝主按钮；
 * - 删除、清空等不可逆操作使用低饱和暖红危险按钮；
 * - 不允许点击遮罩误关闭；
 * - ESC 仍然等价于“取消”，符合桌面应用习惯；
 * - 不展示 Arco 的 simple/icon 模式，避免再次出现与页面风格不一致的大图标。
 *
 * 本函数只负责“询问用户是否继续”，真正的删除/更新仍由调用方在返回 true 后执行，
 * 因而不会改变现有 Store、后端完整性检查或错误处理流程。
 *
 * @param {Object} options
 * @param {string} options.title 对话框标题。
 * @param {string} options.message 需要向用户说明的影响范围。
 * @param {string} [options.confirmText="确认"] 确认按钮文字。
 * @param {string} [options.cancelText="取消"] 取消按钮文字。
 * @param {boolean} [options.danger=false] 是否属于删除/清空等危险操作。
 * @returns {Promise<boolean>} 用户确认时返回 true，其余关闭路径返回 false。
 */
export function confirmAction({
  title,
  message,
  confirmText = "确认",
  cancelText = "取消",
  danger = false,
}) {
  return new Promise((resolve) => {
    let settled = false;

    /**
     * Arco 在按钮回调之后还会继续触发 onClose，
     * 因此需要保证 Promise 只结算一次。
     */
    const settle = (value) => {
      if (settled) {
        return;
      }

      settled = true;
      resolve(Boolean(value));
    };

    Modal.open({
      title: String(title || "确认操作"),
      content: String(message || ""),
      width: 460,
      simple: false,
      titleAlign: "start",
      modalClass: [
        "humbert-confirm-modal",
        danger
            ? "humbert-confirm-modal--danger"
            : "humbert-confirm-modal--normal",
      ],
      bodyClass: "humbert-confirm-modal__body",
      closable: false,
      maskClosable: false,
      escToClose: true,
      hideCancel: false,
      okText: confirmText,
      cancelText,
      okButtonProps: danger
          ? {
              status: "danger",
            }
          : {
              type: "primary",
            },
      onOk: () => {
        settle(true);
      },
      onCancel: () => {
        settle(false);
      },
      onClose: () => {
        // 防御性兜底：如果未来调用方或 Arco 通过其它路径关闭弹窗，按取消处理。
        settle(false);
      },
    });
  });
}
