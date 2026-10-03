import { beginLatestRequest, invalidateRequests } from "../utils/latestRequest.js";
import {
    defineStore,
} from "pinia";

import {
    createSession,
    deleteSession,
    listMessagePage,
    getMessageWindow,
    listSessions,
    renameSession,
    setSessionArchived,
} from "../api/sessions.js";

const DRAFT_STORAGE_KEY =
    "humbert.session-drafts.v1";

const MAX_PERSISTED_DRAFTS = 32;

const MAX_DRAFT_CHARACTERS = 256 * 1024;

const DRAFT_PERSIST_DELAY_MS = 180;

let draftPersistTimer = null;

let pendingDrafts = null;

function loadDrafts() {
    try {
        const value = JSON.parse(
            localStorage.getItem(
                DRAFT_STORAGE_KEY,
            ) ?? "{}",
        );
        if (!value || typeof value !== "object") {
            return {};
        }
        const records =
            Object.entries(value)
                .filter(
                    ([sessionID, record]) =>
                        Boolean(sessionID) &&
                        typeof record?.text ===
                        "string" &&
                        record.text.length <=
                        MAX_DRAFT_CHARACTERS,
                )
                .sort(
                    (left, right) =>
                        Number(
                            right[1]
                                ?.updatedAt ??
                            0,
                        ) -
                        Number(
                            left[1]
                                ?.updatedAt ??
                            0,
                        ),
                )
                .slice(
                    0,
                    MAX_PERSISTED_DRAFTS,
                );
        return Object.fromEntries(records);
    } catch {
        return {};
    }
}

function flushPersistedDrafts() {
    if (draftPersistTimer !== null) {
        clearTimeout(draftPersistTimer);
        draftPersistTimer = null;
    }
    if (!pendingDrafts) {
        return;
    }
    try {
        localStorage.setItem(
            DRAFT_STORAGE_KEY,
            JSON.stringify(pendingDrafts),
        );
    } catch {
        // 草稿持久化不可用时仍保留当前进程内状态，不阻塞聊天。
    } finally {
        pendingDrafts = null;
    }
}

function persistDrafts(drafts) {
    pendingDrafts = drafts;
    if (draftPersistTimer !== null) {
        clearTimeout(draftPersistTimer);
    }
    draftPersistTimer = setTimeout(
        flushPersistedDrafts,
        DRAFT_PERSIST_DELAY_MS,
    );
}

if (typeof window !== "undefined") {
    window.addEventListener(
        "pagehide",
        flushPersistedDrafts,
    );
}

// Promise 不属于可渲染状态；按 Store 实例保存，避免侧栏预加载挤掉主聊天区的请求。
const sessionLoads = new WeakMap();

function invalidateSessionLists(store, agentIDs) {
    const ids = agentIDs ?? new Set([
        store.agentID, ...Object.keys(store.itemsByAgent), ...Object.keys(store.loadingAgents),
    ]);
    // 元数据变更同时使列表请求和等待该列表的选中流程失效，避免删除后再次自动选中。
    for (const id of ids) {
        invalidateRequests(store, `sessions:${id}`);
        sessionLoads.get(store)?.delete(id);
        delete store.loadingAgents[id];
        if (store.agentID === id) {
            store.agentLoadSequence++;
            store.loading = false;
        }
    }
}

// 自动标题只需要在首条用户消息落盘后刷新一次 Sidebar Metadata。
// 该刷新属于辅助 UI，不允许失败后把已经成功的消息同步伪装成发送失败。
const automaticTitleRefreshes = new Set();

/**
 * SessionStore 管理：
 *
 * 1. 当前选中 Agent 的 Session；
 * 2. 当前选中 Session 的 Message History；
 * 3. Sidebar 中各 Agent 的 Session Metadata Cache；
 * 4. Session Search。
 *
 * 重要约束：
 *
 * Session Transcript JSONL 始终是最终事实来源。
 *
 * itemsByAgent 只是前端 Sidebar Cache，不承担持久化职责。
 */
export const useSessionStore =
    defineStore(
        "sessions",
        {
            state: () => ({
                /**
                 * 当前聊天区所属 Agent。
                 */
                agentID: "",
                agentLoadSequence: 0,

                /**
                 * 当前 Agent 的 Session List。
                 *
                 * 继续保留这个字段，避免现有 Chat、
                 * Composer、Rename 等代码大范围修改。
                 */
                items: [],

                /**
                 * agentID -> Session[]
                 *
                 * 用于 Sidebar 二级导航。
                 */
                itemsByAgent: {},

                /**
                 * agentID -> boolean
                 *
                 * 区分：
                 *
                 *   尚未加载
                 *
                 * 与
                 *
                 *   已加载但没有 Session
                 */
                loadedAgents: {},

                /**
                 * agentID -> boolean
                 *
                 * Sidebar 可以据此展示 Agent Session
                 * Metadata 的加载状态。
                 */
                loadingAgents: {},

                selectedID: "",

                messages: [],

                messageHasMore: false,

                // 同一视图的刷新、搜索定位和分页共用版本，旧 IPC 结果不能覆盖新视图。
                messageLoadSequence: 0,

                messageBeforeID: "",

                loadingOlderMessages: false,

                drafts: loadDrafts(),

                search: "",

                jumpTargetID: "",
                searchWindowActive: false,

                loading: false,
            }),

            getters: {
                /**
                 * 当前选中的 Session。
                 */
                selectedSession(state) {
                    return (
                        state.items.find(
                            (session) =>
                                session.id ===
                                state.selectedID,
                        ) ?? null
                    );
                },

                /**
                 * 返回指定 Agent 的 Session Cache。
                 *
                 * Sidebar 使用这个 Getter 构造：
                 *
                 * Agent
                 *   ├── Session A
                 *   └── Session B
                 */
                sessionsForAgent:
                    (state) =>
                        (agentID) => {
                            if (!agentID) {
                                return [];
                            }

                            if (
                                Array.isArray(
                                    state.itemsByAgent[
                                        agentID
                                        ],
                                )
                            ) {
                                return (
                                    state.itemsByAgent[
                                        agentID
                                        ]
                                );
                            }

                            if (
                                state.agentID ===
                                agentID
                            ) {
                                return state.items;
                            }

                            return [];
                        },

                /**
                 * 判断 Agent 的 Session Metadata
                 * 是否已经读取过。
                 */
                isAgentSessionsLoaded:
                    (state) =>
                        (agentID) => {
                            return Boolean(
                                state.loadedAgents[
                                    agentID
                                    ],
                            );
                        },

                /**
                 * 保留原来的当前 Agent Session Search Getter。
                 *
                 * 聊天区其他代码仍然可以继续使用。
                 */
                filteredSessions(state) {
                    const keyword =
                        state.search
                            .trim()
                            .toLowerCase();

                    if (!keyword) {
                        return state.items;
                    }

                    return state.items.filter(
                        (session) =>
                            session.title
                                .toLowerCase()
                                .includes(
                                    keyword,
                                ),
                    );
                },

                draftForSession:
                    (state) =>
                        (sessionID) => {
                            const record =
                                state.drafts[
                                    sessionID
                                    ];
                            return typeof record?.text ===
                            "string"
                                ? record.text
                                : "";
                        },
            },

            actions: {
                /**
                 * 把某个 Agent 的 Session List 写入 Sidebar Cache。
                 *
                 * 如果这个 Agent 同时也是当前聊天 Agent，
                 * 会同步更新传统 items 字段。
                 */
                cacheAgentSessions(
                    agentID,
                    sessions,
                ) {
                    const normalized =
                        Array.isArray(
                            sessions,
                        )
                            ? sessions
                            : [];

                    this.itemsByAgent[
                        agentID
                        ] =
                        normalized;

                    this.loadedAgents[
                        agentID
                        ] =
                        true;

                    if (
                        this.agentID ===
                        agentID
                    ) {
                        this.items =
                            normalized;
                    }
                },

                /**
                 * 只读取某个 Agent 的 Session Metadata。
                 *
                 * 与 loadForAgent() 不同：
                 *
                 * 本方法不会：
                 *
                 *   - 切换当前 Agent；
                 *   - 清空 Message History；
                 *   - 修改 selectedID。
                 *
                 * 因此特别适合 Sidebar 加载折叠 Agent。
                 */
                async loadAgentSessions(
                    agentID,
                    {
                        force = false,
                    } = {},
                ) {
                    if (!agentID) {
                        return [];
                    }

                    let loads = sessionLoads.get(this);
                    if (!loads) {
                        loads = new Map();
                        sessionLoads.set(this, loads);
                    }
                    // 普通读取优先等待正在进行的刷新；force 仍用于保存后的新快照。
                    const pending = loads.get(agentID);
                    if (!force && pending) return pending;

                    if (
                        !force &&
                        this.loadedAgents[
                            agentID
                            ]
                    ) {
                        return (
                            this.itemsByAgent[
                                agentID
                                ] ?? []
                        );
                    }

                    const isCurrent = beginLatestRequest(this, `sessions:${agentID}`);
                    this.loadingAgents[
                        agentID
                        ] =
                        true;

                    const request = (async () => {
                        try {
                            const result = await listSessions(agentID);
                            const sessions = Array.isArray(result) ? result : [];
                            if (!isCurrent()) return null;
                            this.cacheAgentSessions(agentID, sessions);
                            return sessions;
                        } catch (error) {
                            if (isCurrent()) throw error;
                            return null;
                        } finally {
                            if (isCurrent()) delete this.loadingAgents[agentID];
                        }
                    })();
                    loads.set(agentID, request);
                    try {
                        return await request;
                    } finally {
                        // 旧请求结束不能删除后来强制刷新的 Promise。
                        if (loads.get(agentID) === request) loads.delete(agentID);
                    }
                },

                // Agent 入口只读取侧栏目录，始终进入空白页；消息由显式选中会话加载。
                async loadForAgent(
                    agentID,
                ) {
                    const sequence =
                        ++this.agentLoadSequence;

                    // 包括再次点击当前 Agent，立即解除旧会话选择并淘汰在途消息。
                    this.selectedID = "";
                    this.items = this.itemsByAgent[agentID] ?? [];
                    this.agentID =
                        agentID;

                    this.messages = [];
                    this.jumpTargetID = "";
                    this.resetMessagePage();

                    if (!agentID) {
                        this.loading = false;
                        this.items = [];

                        this.selectedID =
                            "";

                        return;
                    }

                    this.loading = true;

                    try {
                        // Sidebar 和 AppShell 可能同时切换同一 Agent，复用正在读取的快照。
                        // 没有进行中的请求时才主动刷新，避免再次打开 Agent 只看到旧缓存。
                        let sessions = await (sessionLoads.get(this)?.get(agentID)
                            ?? this.loadAgentSessions(agentID, { force: true }));

                        // 强制刷新可以替换读取，但不能取消仍有效的 Agent 选择。
                        // 追随最新请求/缓存；删除、重命名等变更会推进选择序列，禁止重新选中旧会话。
                        while (!sessions && sequence === this.agentLoadSequence && this.agentID === agentID) {
                            sessions = await this.loadAgentSessions(agentID);
                        }

                        /**
                         * 如果请求期间用户已经切换到了另外一个 Agent，
                         * 列表只在自身请求仍有效时写入 Cache；主聊天区还需校验选择序列。
                         */
                        if (
                            !sessions || sequence !==
                            this.agentLoadSequence ||
                            this.agentID !==
                            agentID
                        ) {
                            return;
                        }

                        this.items = sessions;
                    } finally {
                        if (
                            sequence ===
                            this.agentLoadSequence
                        ) {
                            this.loading =
                                false;
                        }
                    }
                },

                /**
                 * 为当前 Agent 创建 Conversation。
                 *
                 * 保留原 API。
                 */
                async create() {
                    if (!this.agentID) {
                        throw new Error(
                            "请先选择 Agent",
                        );
                    }

                    return this
                        .createForAgent(
                            this.agentID,
                        );
                },

                /**
                 * 为明确指定的 Agent 创建 Conversation。
                 *
                 * Sidebar 的：
                 *
                 * Agent
                 *   └── + 新建对话
                 *
                 * 使用这个方法。
                 *
                 * 创建成功且用户仍停留在该 Agent 时选中新 Session。
                 */
                async createForAgent(
                    agentID,
                ) {
                    if (!agentID) {
                        throw new Error(
                            "Agent ID 不能为空",
                        );
                    }

                    const selectionSequence = this.agentLoadSequence;
                    const result =
                        await createSession(
                            agentID,
                            "",
                        );

                    const shouldSelect = this.agentID === agentID && this.agentLoadSequence === selectionSequence;
                    invalidateSessionLists(this, [agentID]);
                    const createSequence = this.agentLoadSequence;
                    const sessions =
                        await this
                            .loadAgentSessions(
                                agentID,
                                {
                                    force:
                                        true,
                                },
                            );

                    if (!sessions || !shouldSelect || this.agentID !== agentID || this.agentLoadSequence !== createSequence) return result;
                    this.agentID =
                        agentID;

                    this.items =
                        sessions;

                    this.selectedID =
                        result.id;

                    this.messages = [];

                    this.resetMessagePage();

                    await this
                        .refreshMessages(
                            result.id,
                        );

                    return result;
                },

                /**
                 * 选择当前 Agent 中的 Session。
                 */
                async select(id) {
                    if (
                        !this.items.some(
                            (session) =>
                                session.id ===
                                id,
                        )
                    ) {
                        return;
                    }

                    // 用户明确选择优先于等待目录的 Agent 入口。
                    this.agentLoadSequence++;
                    this.loading = false;
                    this.selectedID =
                        id;

                    this.messages = [];

                    this.resetMessagePage();

                    await this
                        .refreshMessages(id);
                },

                /**
                 * 从 Session Transcript 读取当前 Session 最新一页消息。
                 */
                async refreshMessages(
                    sessionID =
                        this.selectedID,
                ) {
                    if (sessionID && sessionID !== this.selectedID) return;
                    const sequence = ++this.messageLoadSequence;
                    this.loadingOlderMessages = false;
                    if (!sessionID) {
                        this.messages = [];

                        return;
                    }

                    const result =
                        await listMessagePage(
                            sessionID,
                            "",
                            80,
                        );

                    /**
                     * IPC 返回期间用户可能切换 Session。
                     *
                     * 只有仍然处于目标 Session 且请求版本最新时，
                     * 才允许覆盖主 Message List。
                     */
                    if (
                        sequence === this.messageLoadSequence &&
                        sessionID ===
                        this.selectedID
                    ) {
                        // 新窗口落地也淘汰期间发出的旧窗口分页，即使分页游标恰好相同。
                        this.messageLoadSequence++;
                        this.loadingOlderMessages = false;
                        this.messages =
                            Array.isArray(
                                result?.messages,
                            )
                                ? result.messages
                                : [];

                        this.messageHasMore =
                            Boolean(
                                result?.hasMore,
                            );

                        this.messageBeforeID =
                            result?.nextBeforeID ??
                            "";

                        this.searchWindowActive = false;

                        const selected =
                            this.items.find(
                                (session) =>
                                    session.id ===
                                    sessionID,
                            );
                        if (
                            selected?.title ===
                            "新会话" &&
                            !automaticTitleRefreshes.has(
                                sessionID,
                            ) &&
                            this.messages.some(
                                (message) =>
                                    message.role ===
                                    "user",
                            )
                        ) {
                            automaticTitleRefreshes.add(
                                sessionID,
                            );
                            try {
                                await this
                                    .loadAgentSessions(
                                        this.agentID,
                                        {force: true},
                                    );
                            } catch {
                                // 标题刷新失败不影响消息；保留下一次非阻塞重试机会。
                                automaticTitleRefreshes.delete(
                                    sessionID,
                                );
                            }
                        }
                    }
                },

                /**
                 * 向前读取一页历史，并保留当前页已有消息。
                 */
                async loadOlderMessages() {
                    const sessionID =
                        this.selectedID;
                    if (
                        !sessionID ||
                        !this.messageHasMore ||
                        !this.messageBeforeID ||
                        this.loadingOlderMessages
                    ) {
                        return 0;
                    }

                    const beforeID =
                        this.messageBeforeID;
                    const sequence = this.messageLoadSequence;
                    this.loadingOlderMessages =
                        true;
                    try {
                        const result =
                            await listMessagePage(
                                sessionID,
                                beforeID,
                                80,
                            );
                        if (
                            sequence !== this.messageLoadSequence ||
                            sessionID !==
                            this.selectedID ||
                            beforeID !==
                            this.messageBeforeID
                        ) {
                            return 0;
                        }

                        const older =
                            Array.isArray(
                                result?.messages,
                            )
                                ? result.messages
                                : [];
                        const existing =
                            new Set(
                                this.messages.map(
                                    (message) =>
                                        message.id,
                                ),
                            );
                        const unique =
                            older.filter(
                                (message) =>
                                    !existing.has(
                                        message.id,
                                    ),
                            );
                        this.messages = [
                            ...unique,
                            ...this.messages,
                        ];
                        this.messageHasMore =
                            Boolean(
                                result?.hasMore,
                            );
                        this.messageBeforeID =
                            result?.nextBeforeID ??
                            "";
                        return unique.length;
                    } finally {
                        if (
                            sequence === this.messageLoadSequence &&
                            sessionID ===
                            this.selectedID
                        ) {
                            this.loadingOlderMessages =
                                false;
                        }
                    }
                },

                async loadSearchWindow(entryID) {
                    const sessionID = this.selectedID;
                    if (!sessionID || !entryID) return false;
                    const sequence = ++this.messageLoadSequence;
                    this.loadingOlderMessages = false;
                    const result = await getMessageWindow(sessionID, entryID);
                    if (sequence !== this.messageLoadSequence || sessionID !== this.selectedID || this.jumpTargetID !== entryID) return false;
                    this.messageLoadSequence++;
                    this.loadingOlderMessages = false;
                    this.messages = Array.isArray(result?.messages) ? result.messages : [];
                    this.messageHasMore = Boolean(result?.hasMore);
                    this.messageBeforeID = result?.nextBeforeID ?? "";
                    this.searchWindowActive = true;
                    return true;
                },

                resetMessagePage() {
                    this.messageLoadSequence++;
                    this.messageHasMore = false;
                    this.messageBeforeID = "";
                    this.loadingOlderMessages = false;
                    this.searchWindowActive = false;
                },

                setDraft(sessionID, text) {
                    if (!sessionID) {
                        return;
                    }
                    const normalized =
                        typeof text === "string"
                            ? text
                            : "";
                    if (!normalized) {
                        delete this.drafts[
                            sessionID
                            ];
                    } else {
                        this.drafts[sessionID] = {
                            text: normalized,
                            updatedAt: Date.now(),
                        };
                    }

                    const retained =
                        Object.entries(
                            this.drafts,
                        )
                            .sort(
                                (left, right) =>
                                    Number(
                                        right[1]
                                            ?.updatedAt ??
                                        0,
                                    ) -
                                    Number(
                                        left[1]
                                            ?.updatedAt ??
                                        0,
                                    ),
                            )
                            .slice(
                                0,
                                MAX_PERSISTED_DRAFTS,
                            );
                    this.drafts =
                        Object.fromEntries(
                            retained,
                        );
                    persistDrafts(
                        this.drafts,
                    );
                },

                clearDraft(sessionID) {
                    this.setDraft(
                        sessionID,
                        "",
                    );
                    // 已发送或已删除的草稿必须立即落盘，避免快速退出后重新出现。
                    flushPersistedDrafts();
                },

                /**
                 * 修改 Session Title。
                 *
                 * 与原实现相比，现在除了更新当前 items，
                 * 还会同步更新 itemsByAgent Cache。
                 *
                 * 因此即使用户在 Sidebar 中重命名的是另一个
                 * 展开 Agent 下的 Session，也能立即反映到 UI。
                 */
                async rename(
                    id,
                    title,
                ) {
                    const normalized =
                        title.trim();

                    if (!normalized) {
                        throw new Error(
                            "会话名称不能为空",
                        );
                    }

                    const result =
                        await renameSession(
                            id,
                            normalized,
                        );

                    invalidateSessionLists(this, result.agentID ? [result.agentID] : undefined);
                    this.items =
                        this.items.map(
                            (session) =>
                                session.id ===
                                id
                                    ? result
                                    : session,
                        );

                    for (
                        const agentID of
                        Object.keys(
                            this.itemsByAgent,
                        )
                        ) {
                        const sessions =
                            this.itemsByAgent[
                                agentID
                                ];

                        if (
                            !Array.isArray(
                                sessions,
                            )
                        ) {
                            continue;
                        }

                        this.itemsByAgent[
                            agentID
                            ] =
                            sessions.map(
                                (session) =>
                                    session.id ===
                                    id
                                        ? result
                                        : session,
                            );
                    }

                    return result;
                },

                async setArchived(id, archived) {
                    const result = await setSessionArchived(id, archived);
                    invalidateSessionLists(this, result.agentID ? [result.agentID] : undefined);
                    this.items = this.items.map((session) => session.id === id ? result : session);
                    for (const agentID of Object.keys(this.itemsByAgent)) {
                        this.itemsByAgent[agentID] = this.itemsByAgent[agentID].map((session) => session.id === id ? result : session);
                    }
                    if (archived && this.selectedID === id) {
                        const next = this.items.find((session) => !session.archived && session.id !== id);
                        this.selectedID = next?.id || "";
                        this.messages = [];
                        this.resetMessagePage();
                        if (next) await this.refreshMessages(next.id);
                    }
                    return result;
                },

                /**
                 * 清理由其他领域已经删除的 Session 前端缓存。
                 *
                 * TaskRun 删除会话文件以后调用这里，不能再次请求
                 * SessionService.Delete，否则会因为文件已经不存在而失败。
                 */
                async forgetSessions(ids) {
                    const forgotten =
                        new Set(
                            (Array.isArray(ids)
                                ? ids
                                : [ids]
                            ).filter(Boolean),
                        );
                    if (forgotten.size === 0) {
                        return;
                    }

                    // Task 删除可能传来尚未缓存的 Session ID，保守淘汰所有在途目录。
                    invalidateSessionLists(this);
                    const wasSelected =
                        forgotten.has(
                            this.selectedID,
                        );

                    this.items =
                        this.items.filter(
                            (session) =>
                                !forgotten.has(
                                    session.id,
                                ),
                        );

                    for (
                        const agentID of
                        Object.keys(
                            this.itemsByAgent,
                        )
                        ) {
                        const sessions =
                            this.itemsByAgent[
                                agentID
                                ];

                        if (!Array.isArray(sessions)) {
                            continue;
                        }

                        this.itemsByAgent[
                            agentID
                            ] =
                            sessions.filter(
                                (session) =>
                                    !forgotten.has(
                                        session.id,
                                    ),
                            );
                    }

                    for (const id of forgotten) {
                        this.clearDraft(id);
                        automaticTitleRefreshes.delete(id);
                    }

                    if (!wasSelected) {
                        return;
                    }

                    this.selectedID =
                        this.items[0]?.id ??
                        "";
                    this.messages = [];
                    this.resetMessagePage();

                    if (this.selectedID) {
                        await this.refreshMessages(
                            this.selectedID,
                        );
                    }
                },

                /**
                 * 删除 Session。
                 *
                 * 删除以后同步清理所有 Agent Cache。
                 *
                 * 如果删除的是当前 Session，则自动选择当前
                 * Agent 中剩余的第一条 Session。
                 */
                async remove(id) {
                    await deleteSession(id);
                    await this.forgetSessions([id]);
                },

                /**
                 * Agent 被删除以后清理前端 Cache。
                 *
                 * Workspace 文件不在这里处理。
                 */
                forgetAgent(agentID) {
                    if (!agentID) {
                        return;
                    }

                    invalidateSessionLists(this, [agentID]);
                    const forgottenSessions =
                        this.itemsByAgent[
                            agentID
                            ] ?? [];
                    for (const session of forgottenSessions) {
                        this.clearDraft(
                            session.id,
                        );
                        automaticTitleRefreshes.delete(
                            session.id,
                        );
                    }

                    delete this
                        .itemsByAgent[
                        agentID
                        ];

                    delete this
                        .loadedAgents[
                        agentID
                        ];

                    delete this
                        .loadingAgents[
                        agentID
                        ];

                    if (
                        this.agentID !==
                        agentID
                    ) {
                        return;
                    }

                    this.agentID = "";

                    this.items = [];

                    this.selectedID = "";

                    this.messages = [];

                    this.resetMessagePage();
                },
            },
        },
    );
