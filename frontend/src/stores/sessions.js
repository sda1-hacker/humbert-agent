import {
    defineStore,
} from "pinia";

import {
    createSession,
    deleteSession,
    listMessages,
    listSessions,
    renameSession,
} from "../api/sessions.js";

/**
 * loadForAgentSequence 用于防止快速切换 Agent 时出现旧请求覆盖新状态。
 *
 * 例如：
 *
 * Agent A
 *    ↓ 请求尚未返回
 * Agent B
 *    ↓ 请求先返回
 * Agent A
 *    ↓ 旧请求最后才返回
 *
 * 如果不做序列保护，A 的 Session List 有可能覆盖 B。
 */
let loadForAgentSequence = 0;

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

                search: "",

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

                    this.loadingAgents[
                        agentID
                        ] =
                        true;

                    try {
                        const result =
                            await listSessions(
                                agentID,
                            );

                        const sessions =
                            Array.isArray(
                                result,
                            )
                                ? result
                                : [];

                        this.cacheAgentSessions(
                            agentID,
                            sessions,
                        );

                        return sessions;
                    } finally {
                        delete this
                            .loadingAgents[
                            agentID
                            ];
                    }
                },

                /**
                 * 切换当前 Agent。
                 *
                 * 这个 API 保持原来的行为：
                 *
                 *   Agent 切换
                 *       ↓
                 *   加载该 Agent Sessions
                 *       ↓
                 *   如果旧 selectedID 不属于它
                 *       ↓
                 *   默认选择第一条 Session
                 *       ↓
                 *   加载 Message History
                 *
                 * 同时增加请求序列保护，防止快速切换导致数据串台。
                 */
                async loadForAgent(
                    agentID,
                ) {
                    const sequence =
                        ++loadForAgentSequence;

                    this.agentID =
                        agentID;

                    this.messages = [];

                    if (!agentID) {
                        this.items = [];

                        this.selectedID =
                            "";

                        return;
                    }

                    this.loading = true;

                    try {
                        const sessions =
                            await this
                                .loadAgentSessions(
                                    agentID,
                                    {
                                        force:
                                            true,
                                    },
                                );

                        /**
                         * 如果请求期间用户已经切换到了另外一个 Agent，
                         * 当前结果只允许留在 Cache，
                         * 不允许覆盖主聊天区。
                         */
                        if (
                            sequence !==
                            loadForAgentSequence ||
                            this.agentID !==
                            agentID
                        ) {
                            return;
                        }

                        this.items =
                            sessions;

                        const selectedExists =
                            sessions.some(
                                (session) =>
                                    session.id ===
                                    this.selectedID,
                            );

                        if (!selectedExists) {
                            this.selectedID =
                                sessions[0]
                                    ?.id ??
                                "";
                        }

                        if (
                            this.selectedID
                        ) {
                            await this
                                .refreshMessages(
                                    this.selectedID,
                                );
                        }
                    } finally {
                        if (
                            sequence ===
                            loadForAgentSequence
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
                 * 创建成功后该 Agent 会成为当前 Agent，
                 * 新 Session 会成为当前 Session。
                 */
                async createForAgent(
                    agentID,
                ) {
                    if (!agentID) {
                        throw new Error(
                            "Agent ID 不能为空",
                        );
                    }

                    const result =
                        await createSession(
                            agentID,
                            "",
                        );

                    const sessions =
                        await this
                            .loadAgentSessions(
                                agentID,
                                {
                                    force:
                                        true,
                                },
                            );

                    this.agentID =
                        agentID;

                    this.items =
                        sessions;

                    this.selectedID =
                        result.id;

                    this.messages = [];

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

                    this.selectedID =
                        id;

                    await this
                        .refreshMessages(id);
                },

                /**
                 * 从 Session Transcript 读取当前 Session 最近消息。
                 */
                async refreshMessages(
                    sessionID =
                        this.selectedID,
                ) {
                    if (!sessionID) {
                        this.messages = [];

                        return;
                    }

                    const result =
                        await listMessages(
                            sessionID,
                            200,
                        );

                    /**
                     * IPC 返回期间用户可能切换 Session。
                     *
                     * 只有仍然处于目标 Session 时，
                     * 才允许覆盖主 Message List。
                     */
                    if (
                        sessionID ===
                        this.selectedID
                    ) {
                        this.messages =
                            Array.isArray(
                                result,
                            )
                                ? result
                                : [];
                    }
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

                    const wasSelected =
                        this.selectedID ===
                        id;

                    this.items =
                        this.items.filter(
                            (session) =>
                                session.id !==
                                id,
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
                            sessions.filter(
                                (session) =>
                                    session.id !==
                                    id,
                            );
                    }

                    if (!wasSelected) {
                        return;
                    }

                    this.selectedID =
                        this.items[0]?.id ??
                        "";

                    this.messages = [];

                    if (
                        this.selectedID
                    ) {
                        await this
                            .refreshMessages(
                                this.selectedID,
                            );
                    }
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
                },
            },
        },
    );
