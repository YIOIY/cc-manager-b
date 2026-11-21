import { createApp } from 'vue'
import { vLoading } from 'element-plus'
import 'element-plus/theme-chalk/base.css'
import 'element-plus/es/components/loading/style/css'
import 'element-plus/es/components/message-box/style/css'
import 'element-plus/es/components/message/style/css'
import './styles/fonts.css'
import './styles/base.css'
import './styles/shared.css'
import App from './App.vue'
import { router } from './router'

createApp(App).use(router).directive('loading', vLoading).mount('#app')
