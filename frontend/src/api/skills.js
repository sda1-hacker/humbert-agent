import {
    Call,
} from "@wailsio/runtime";

const skillServiceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.SkillService";

/**
 * 读取 Skills Root、安装包元数据、Alias 与 Agent enabled_skills 投影。
 *
 * Catalog 不携带 SKILL.md 正文；只有用户进入详情并点选文本文件时才会按需读取。
 */
export function getSkillState() {
    return Call.ByName(
        `${skillServiceName}.State`,
    );
}


/**
 * 按需读取一个有效 Skill 的详情与文件树。
 *
 * 返回值只包含文件路径/大小/文本标记，不包含文件正文。
 */
export function getSkillDetail(name) {
    return Call.ByName(
        `${skillServiceName}.SkillDetail`,
        name,
    );
}

/**
 * 按需预览一个已安装 Skill 包内的 UTF-8 文本文件。
 *
 * 路径安全、symlink、二进制和哈希校验全部在 Go 侧完成；scripts/ 只读取，不执行。
 */
export function readSkillFile(name, relativePath) {
    return Call.ByName(
        `${skillServiceName}.ReadSkillFile`,
        name,
        relativePath,
    );
}

/**
 * 打开 Wails 原生目录选择器。空字符串表示用户取消。
 */
export function selectSkillDirectory(currentPath = "") {
    return Call.ByName(
        `${skillServiceName}.SelectSkillDirectory`,
        currentPath,
    );
}

/**
 * 从用户明确选择的本地目录安装一个 Skill Package。
 */
export function installSkill(sourceDirectory) {
    return Call.ByName(
        `${skillServiceName}.InstallSkill`,
        sourceDirectory,
    );
}

/**
 * 从公开 HTTPS Skill 来源安装 Skill。
 *
 * Go 后端通过可扩展 Resolver Registry 识别来源；内置支持 Direct ZIP、GitHub、GitLab.com、Gitee 与 skills.sh。
 * skillPath 只在归档中包含多个 SKILL.md 时需要；所有网络、ZIP 与包结构安全校验均在 Go 后端执行。
 */
export function installSkillFromURL(sourceURL, skillPath = "") {
    return Call.ByName(
        `${skillServiceName}.InstallSkillFromURL`,
        sourceURL,
        skillPath,
    );
}

/**
 * 扫描公开 HTTPS Skill 来源中的一个或多个 SKILL.md，不修改本地 Catalog。
 *
 * sourceURL 可以是仓库、Skill 详情页或 ZIP；skillPath 仅作为可选扫描范围。
 */
export function discoverSkillSource(sourceURL, skillPath = "") {
    return Call.ByName(
        `${skillServiceName}.DiscoverSkillSource`,
        sourceURL,
        skillPath,
    );
}

/**
 * 扫描本地目录中的 Skills，不执行安装。
 */
export function discoverLocalSkillSource(sourceDirectory) {
    return Call.ByName(
        `${skillServiceName}.DiscoverLocalSkillSource`,
        sourceDirectory,
    );
}

/**
 * 从一次远程 Discovery 中选择多个 Skill 原子安装。
 */
export function installDiscoveredSkillsFromURL(sourceURL, paths) {
    return Call.ByName(
        `${skillServiceName}.InstallDiscoveredSkillsFromURL`,
        sourceURL,
        Array.isArray(paths) ? paths : [],
    );
}

/**
 * 从一次本地 Discovery 中选择多个 Skill 原子安装。
 */
export function installDiscoveredSkillsFromDirectory(sourceDirectory, paths) {
    return Call.ByName(
        `${skillServiceName}.InstallDiscoveredSkillsFromDirectory`,
        sourceDirectory,
        Array.isArray(paths) ? paths : [],
    );
}

/**
 * 设置或清除用户自己的 Skill 展示别名。
 *
 * Alias 只影响 Humbert UI，不写入 SKILL.md，也不改变 canonical name / enabled_skills。
 */
export function setSkillAlias(name, alias = "") {
    return Call.ByName(
        `${skillServiceName}.SetSkillAlias`,
        name,
        alias,
    );
}

/**
 * 让某个 Agent 从下一 Turn 开始启用 Skill。
 */
export function enableSkillForAgent(skillName, agentID) {
    return Call.ByName(
        `${skillServiceName}.EnableSkillForAgent`,
        skillName,
        agentID,
    );
}

/**
 * 从某个 Agent 的 config.json enabled_skills 中移除 Skill。
 */
export function disableSkillForAgent(skillName, agentID) {
    return Call.ByName(
        `${skillServiceName}.DisableSkillForAgent`,
        skillName,
        agentID,
    );
}

/**
 * 删除一个未被任何 Agent 引用的已安装 Skill。
 */
export function deleteSkill(name) {
    return Call.ByName(
        `${skillServiceName}.DeleteSkill`,
        name,
    );
}

/**
 * 从安装时记录的来源重新读取候选 Package，只比较 Identity，不修改当前安装。
 */
export function checkSkillUpdate(name) {
    return Call.ByName(
        `${skillServiceName}.CheckSkillUpdate`,
        name,
    );
}

/**
 * 从已记录来源更新 Skill。内容没有变化时后端不会做目录替换。
 */
export function updateSkill(name) {
    return Call.ByName(
        `${skillServiceName}.UpdateSkill`,
        name,
    );
}

/**
 * 从已记录来源强制重新安装 Skill。当前 Package 已损坏时，这也是安全修复入口；
 * 候选包会先完整校验，再原子替换当前目录。
 */
export function reinstallSkill(name) {
    return Call.ByName(
        `${skillServiceName}.ReinstallSkill`,
        name,
    );
}

/**
 * 从新的远程 URL 原子重新安装现有 Skill，并建立/更换来源记录。
 * 当前 Skill 无效时允许作为修复操作，但 canonical name 必须保持一致。
 */
export function reinstallSkillFromURL(name, sourceURL, skillPath = "") {
    return Call.ByName(
        `${skillServiceName}.ReinstallSkillFromURL`,
        name,
        sourceURL,
        skillPath,
    );
}

/**
 * 从新的本地目录原子重新安装现有 Skill，并建立/更换来源记录。
 * 当前 Skill 无效时允许作为修复操作，但 canonical name 必须保持一致。
 */
export function reinstallSkillFromDirectory(name, sourceDirectory) {
    return Call.ByName(
        `${skillServiceName}.ReinstallSkillFromDirectory`,
        name,
        sourceDirectory,
    );
}
