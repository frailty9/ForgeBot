import '@fontsource/ibm-plex-mono/latin-400.css'
import '@fontsource/ibm-plex-mono/latin-500.css'
import 'virtual:uno.css'
import '@/assets/styles/main.scss'

import { createPinia } from 'pinia'
import { createVaporApp } from 'vue'

import AdminApp from '@/app/AdminApp.vue'

createVaporApp(AdminApp).use(createPinia()).mount('#app')
