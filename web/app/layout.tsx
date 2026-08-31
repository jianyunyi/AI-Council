import Link from 'next/link'
import './globals.css'

export default function Layout({ children }: { children: React.ReactNode }) {
  return <html lang="zh-CN"><body><header><strong>AI Council</strong><nav><Link href="/">任务</Link><Link href="/providers">模型</Link><Link href="/workspaces">工作区</Link></nav></header><main>{children}</main></body></html>
}
