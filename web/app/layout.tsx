import type {Metadata, Viewport} from 'next'
import './globals.css'
import {AppShell} from '@/components/app-shell'

export const metadata: Metadata = {
  title: 'AI Council 控制台',
  description: '多模型协商与受控执行控制台',
  other: {'color-scheme': 'light dark'},
}
export const viewport: Viewport = {colorScheme: 'light dark'}

export default function Layout({ children }: { children: React.ReactNode }) {
  return <html lang="zh-CN" data-theme="dark" suppressHydrationWarning><body><AppShell>{children}</AppShell></body></html>
}
