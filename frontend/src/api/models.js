import { Call } from "@wailsio/runtime";

/**
 * Model Registry 前端 API Adapter。
 *
 * 使用 ByName 而不是生成 Binding 的 ByID/DTO createFrom，这样模型 Capability DTO 在
 * Go 侧演进时不要求提交生成物；wails3 generate bindings 仍可在开发机按需运行。
 */
const serviceName =
    "github.com/sda1-hacker/humbert-agent/internal/services.ModelService";

export function getModelState() {
    return Call.ByName(`${serviceName}.State`);
}

export function createProvider(request) {
    return Call.ByName(`${serviceName}.CreateProvider`, request);
}

export function updateProvider(id, request) {
    return Call.ByName(`${serviceName}.UpdateProvider`, id, request);
}

export function deleteProvider(id) {
    return Call.ByName(`${serviceName}.DeleteProvider`, id);
}

export function createModel(request) {
    return Call.ByName(`${serviceName}.CreateModel`, request);
}

export function updateModel(id, request) {
    return Call.ByName(`${serviceName}.UpdateModel`, id, request);
}

export function deleteModel(id) {
    return Call.ByName(`${serviceName}.DeleteModel`, id);
}

export function testModel(id) {
    return Call.ByName(`${serviceName}.TestModel`, id);
}

export function updateMultimediaConfig(request) {
    return Call.ByName(`${serviceName}.UpdateMultimediaConfig`, request);
}
