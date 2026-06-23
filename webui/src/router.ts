import { createRouter, createWebHashHistory } from 'vue-router'
import Dashboard from './views/Dashboard.vue'
import ChatList from './views/ChatList.vue'
import ChatDetail from './views/ChatDetail.vue'

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', name: 'dashboard', component: Dashboard },
    { path: '/chats', name: 'chats', component: ChatList },
    {
      path: '/chats/:chatID',
      name: 'chat-detail',
      component: ChatDetail,
      props: (r) => ({
        chatID: String(r.params.chatID),
        botID: typeof r.query.bot === 'string' ? r.query.bot : undefined,
      }),
    },
  ],
})
