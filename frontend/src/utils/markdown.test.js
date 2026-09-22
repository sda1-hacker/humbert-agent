import assert from "node:assert/strict";
import test from "node:test";

import { parseInline } from "./markdown.js";

test("远程图片需要用户主动打开，本地内嵌图片仍可显示", () => {
    const remote = parseInline("![tracking](https://example.com/pixel.png)");
    assert.deepEqual(remote, [{ type: "remote-image", alt: "tracking", url: "https://example.com/pixel.png" }]);
    const local = parseInline("![chart](data:image/png;base64,YQ==)");
    assert.deepEqual(local, [{ type: "image", alt: "chart", url: "data:image/png;base64,YQ==" }]);
    const unsafe = parseInline("![bad](javascript:alert)");
    assert.equal(unsafe[0].type, "text");
});
