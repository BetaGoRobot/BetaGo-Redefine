import { createRouter, createWebHashHistory } from 'vue-router'
import ChatList from './views/ChatList.vue'
import ChatDetail from './views/ChatDetail.vue'

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'chats', component: ChatList },
    { path: '/chats/:chatID', name: 'chat-detail', component: ChatDetail, props: true },
  ],
})
