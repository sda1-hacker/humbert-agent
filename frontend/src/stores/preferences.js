import { defineStore } from "pinia";
import {
    getUserProfile,
    updateUserProfile,
    saveLanguage,
} from "../api/preferences.js";
import { language, setLanguage } from "../i18n/index.js";

export const usePreferenceStore = defineStore("preferences", {
    state: () => ({
        user: { name: "你", avatar: "", language: language.value },
        loading: false,
    }),

    actions: {
        async load() {
            this.loading = true;
            try {
                const value = await getUserProfile();
                this.user = {
                    name: value?.name || "你",
                    avatar: value?.avatar || "",
                    language: value?.language || "zh-CN",
                };
                setLanguage(this.user.language);
                return this.user;
            } finally {
                this.loading = false;
            }
        },

        async updateUser(request) {
            const value = await updateUserProfile({ ...request, language: this.user.language });
            this.user = {
                name: value?.name || "你",
                avatar: value?.avatar || "",
                language: value?.language || this.user.language,
            };
            return this.user;
        },

        async updateLanguage(nextLanguage) {
            const value = await saveLanguage(nextLanguage);
            this.user = {
                name: value?.name || this.user.name,
                avatar: value?.avatar || this.user.avatar,
                language: value?.language || nextLanguage,
            };
            setLanguage(this.user.language);
            return this.user.language;
        },
    },
});
