import { writeFileSync } from "node:fs";

// Vite 清空 dist 后重建该占位文件，避免构建一次就出现跟踪文件删除。
writeFileSync(new URL("../dist/.gitkeep", import.meta.url), "");
