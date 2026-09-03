import Link from 'next/link'
import './globals.css'

export default function Layout({ children }: { children: React.ReactNode }) {
  return <html lang="zh-CN"><body className="app-shell"><header className="app-shell__header"><div className="app-shell__brand"><strong>AI Council</strong><span>多模型协商与受控执行</span></div><nav aria-label="主导航"><Link href="/">任务</Link><Link href="/providers">模型</Link><Link href="/workspaces">工作区</Link></nav></header><main className="app-shell__main">{children}</main></body></html>
}
