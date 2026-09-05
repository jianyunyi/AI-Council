import {defineConfig} from '@playwright/test'
const windows=process.platform==='win32'
export default defineConfig({
  testDir:'./e2e',
  timeout:60_000,
  use:{
    baseURL:'http://127.0.0.1:3000',
    channel:windows?'msedge':undefined,
    launchOptions:windows?{ignoreDefaultArgs:['--headless=old'],args:['--headless=new']}:undefined,
  },
  webServer:{command:'node e2e/server.mjs',url:'http://127.0.0.1:3000',reuseExistingServer:false,timeout:180_000},
})
