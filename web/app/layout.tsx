import './globals.css'
import {AppShell} from '@/components/app-shell'

export default function Layout({ children }: { children: React.ReactNode }) {
  return <html lang="zh-CN"><body className="app-shell"><AppShell>{children}</AppShell></body></html>
}
