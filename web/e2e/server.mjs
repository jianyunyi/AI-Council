import {mkdtempSync,mkdirSync,rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {dirname,join,resolve} from 'node:path'
import {fileURLToPath} from 'node:url'
import {spawn,spawnSync} from 'node:child_process'

const webDir=resolve(dirname(fileURLToPath(import.meta.url)),'..')
const repoDir=resolve(webDir,'..')
const ownedRoot=mkdtempSync(join(tmpdir(),'aicouncil-e2e-'))
const cacheDir=join(ownedRoot,'go-cache')
const binary=join(ownedRoot,process.platform==='win32'?'council-e2e.exe':'council-e2e')
const dbPath=join(ownedRoot,'council.db')
const children=[]
let stopping=false
mkdirSync(cacheDir,{recursive:true})

function stop(code=0){
 if(stopping)return
 stopping=true
 for(const child of [...children].reverse())if(child.exitCode===null)child.kill()
 setTimeout(()=>{rmSync(ownedRoot,{recursive:true,force:true});process.exit(code)},500).unref()
}
process.on('SIGINT',()=>stop(0))
process.on('SIGTERM',()=>stop(0))
process.on('uncaughtException',error=>{console.error(error);stop(1)})
process.on('unhandledRejection',error=>{console.error(error);stop(1)})

const built=spawnSync('go',['build','-o',binary,'./cmd/council-server'],{
 cwd:repoDir,
 env:{...process.env,GOCACHE:cacheDir},
 stdio:'inherit',
})
if(built.status!==0){rmSync(ownedRoot,{recursive:true,force:true});process.exit(built.status??1)}

const council=spawn(binary,[
 '--listen=127.0.0.1:18081',`--db=${dbPath}`,'--rbac=true','--rbac-bootstrap-subject=admin',
],{
 cwd:repoDir,
 env:{...process.env,AUTH_COOKIE_SECURE:'false',COUNCIL_BOOTSTRAP_PASSWORD:'e2e-admin-password'},
 stdio:'inherit',
})
children.push(council)
council.once('exit',code=>{if(!stopping)stop(code??1)})

async function waitFor(url){
 const deadline=Date.now()+90_000
 while(Date.now()<deadline){
  try{const response=await fetch(url);if(response.ok)return}catch{}
  await new Promise(resolve=>setTimeout(resolve,250))
 }
 throw new Error(`timed out waiting for ${url}`)
}

await waitFor('http://127.0.0.1:18081/healthz')
const nextBin=resolve(webDir,'node_modules','next','dist','bin','next')
const next=spawn(process.execPath,[nextBin,'dev','-p','3000'],{
 cwd:webDir,
 env:{...process.env,E2E_API_ORIGIN:'http://127.0.0.1:18081'},
 stdio:'inherit',
})
children.push(next)
next.once('exit',code=>{if(!stopping)stop(code??1)})
