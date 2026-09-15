/**
 * Model Registry 前端 API Adapter。
 *
 * Vue Component 不直接 import Wails generated bindings，
 * 统一通过本模块访问 ModelService。
 */

let bindingPromise = null;

/**
 * 延迟加载 Wails ModelService。
 *
 * 使用缓存 Promise 可以避免每一次 UI 操作都重新执行 dynamic import，
 * 同时 Binding 加载失败不会阻止 Vue SPA 本身完成 mount。
 */
function loadBinding() {
    if (!bindingPromise) {
        bindingPromise = import(
            "../../bindings/github.com/sda1-hacker/humbert-agent/internal/services/modelservice.js"
            ).catch((error) => {
            bindingPromise = null;

            console.error(
                "[Humbert] ModelService Binding 加载失败",
                error,
            );

            throw new Error(
                "无法加载 ModelService Binding，请重新执行 wails3 generate bindings",
                {
                    cause: error,
                },
            );
        });
    }

    return bindingPromise;
}

export async function getModelState() {
    const binding = await loadBinding();

    return binding.State();
}

export async function createProvider(
    request,
) {
    const binding = await loadBinding();

    return binding.CreateProvider(request);
}

export async function updateProvider(
    id,
    request,
) {
    const binding = await loadBinding();

    return binding.UpdateProvider(
        id,
        request,
    );
}

export async function deleteProvider(
    id,
) {
    const binding = await loadBinding();

    return binding.DeleteProvider(id);
}

export async function createModel(
    request,
) {
    const binding = await loadBinding();

    return binding.CreateModel(request);
}

export async function updateModel(
    id,
    request,
) {
    const binding = await loadBinding();

    return binding.UpdateModel(
        id,
        request,
    );
}

export async function deleteModel(
    id,
) {
    const binding = await loadBinding();

    return binding.DeleteModel(id);
}

export async function testModel(
    id,
) {
    const binding = await loadBinding();

    return binding.TestModel(id);
}