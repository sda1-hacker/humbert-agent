import { defineStore } from "pinia";
import {
    getUserProfile,
    updateUserProfile,
} from "../api/preferences.js";

export const usePreferenceStore = defineStore("preferences", {
    state: () => ({
        user: { name: "你", avatar: "" },
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
                };
                return this.user;
            } finally {
                this.loading = false;
            }
        },

        async updateUser(request) {
            const value = await updateUserProfile(request);
            this.user = {
                name: value?.name || "你",
                avatar: value?.avatar || "",
            };
            return this.user;
        },
    },
});
