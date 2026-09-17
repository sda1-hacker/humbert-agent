import Alert from "@arco-design/web-vue/es/alert/index.js";
import Button from "@arco-design/web-vue/es/button/index.js";
import Checkbox from "@arco-design/web-vue/es/checkbox/index.js";
import Dropdown from "@arco-design/web-vue/es/dropdown/index.js";
import Form from "@arco-design/web-vue/es/form/index.js";
import Input from "@arco-design/web-vue/es/input/index.js";
import InputNumber from "@arco-design/web-vue/es/input-number/index.js";
import Modal from "@arco-design/web-vue/es/modal/index.js";
import Radio from "@arco-design/web-vue/es/radio/index.js";
import Select from "@arco-design/web-vue/es/select/index.js";
import Space from "@arco-design/web-vue/es/space/index.js";
import Spin from "@arco-design/web-vue/es/spin/index.js";
import Switch from "@arco-design/web-vue/es/switch/index.js";
import Tabs from "@arco-design/web-vue/es/tabs/index.js";
import Tag from "@arco-design/web-vue/es/tag/index.js";
import Textarea from "@arco-design/web-vue/es/textarea/index.js";
import Tooltip from "@arco-design/web-vue/es/tooltip/index.js";

import "@arco-design/web-vue/es/alert/style/css.js";
import "@arco-design/web-vue/es/button/style/css.js";
import "@arco-design/web-vue/es/checkbox/style/css.js";
import "@arco-design/web-vue/es/dropdown/style/css.js";
import "@arco-design/web-vue/es/form/style/css.js";
import "@arco-design/web-vue/es/input/style/css.js";
import "@arco-design/web-vue/es/input-number/style/css.js";
import "@arco-design/web-vue/es/message/style/css.js";
import "@arco-design/web-vue/es/modal/style/css.js";
import "@arco-design/web-vue/es/radio/style/css.js";
import "@arco-design/web-vue/es/select/style/css.js";
import "@arco-design/web-vue/es/space/style/css.js";
import "@arco-design/web-vue/es/spin/style/css.js";
import "@arco-design/web-vue/es/switch/style/css.js";
import "@arco-design/web-vue/es/tabs/style/css.js";
import "@arco-design/web-vue/es/tag/style/css.js";
import "@arco-design/web-vue/es/textarea/style/css.js";
import "@arco-design/web-vue/es/tooltip/style/css.js";

const components = [
  Alert,
  Button,
  Checkbox,
  Dropdown,
  Form,
  Input,
  InputNumber,
  Modal,
  Radio,
  Select,
  Space,
  Spin,
  Switch,
  Tabs,
  Tag,
  Textarea,
  Tooltip,
];

// 只注册模板实际使用的 Arco 组件。各插件会同时注册自己的子组件，例如
// FormItem、Option、Doption、TabPane 与 InputPassword。
export function installArco(app) {
  for (const component of components) {
    app.use(component);
  }
}
