import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig({plugins:[vue()],build:{rollupOptions:{input:{main:'index.html',qrDisplay:'qr-display.html'}}},server:{proxy:{'/api':'http://localhost:31080','/media':'http://localhost:31080'}}})
