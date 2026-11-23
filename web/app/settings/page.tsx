'use client'

import {useEffect, useState} from 'react'
import {usePrefs} from '@/components/theme'
import {ConfirmDialog, ConfirmState, showToast} from '@/components/ui'
import {apiBase} from '@/lib/desktop'
import {desktopBridge, DesktopStatus} from '@/lib/desktop-bridge'
import {listTaskIds} from '@/lib/task-index'

function RadioRow<T extends string>({label, value, options, onChange}: {
  label: string
  value: T
  options: readonly (readonly [T, string])[]
  onChange: (next: T) => void
}) {
  return (
    <div>
      <h3 style={{margin: '0 0 8px', fontSize: 15}}>{label}</h3>
      <div className="radio-row" role="radiogroup" aria-label={label}>
        {options.map(([option, text]) => (
          <button key={option} role="radio" aria-checked={value === option}
            className={value === option ? '' : 'secondary'} onClick={() => onChange(option)}>{text}</button>
        ))}
      </div>
    </div>
  )
}

export default function SettingsPage() {
  const {theme, setTheme, density, setDensity, diffView, setDiffView} = usePrefs()
  const bridge = desktopBridge()
  const [service, setService] = useState<DesktopStatus>()
  const [serviceBusy, setServiceBusy] = useState(false)
  const [serviceMessage, setServiceMessage] = useState('')
  const [destination, setDestination] = useState('')
  const [health, setHealth] = useState('')
  const [indexCount, setIndexCount] = useState(0)
  const [confirm, setConfirm] = useState<ConfirmState | null>(null)

  useEffect(() => { setIndexCount(listTaskIds().length) }, [])
  useEffect(() => {
    if (!bridge) return
    bridge.status().then(setService).catch(() => {})
  }, [bridge])

  async function checkHealth() {
    setHealth('正在检查…')
    try {
      const root = apiBase().replace(/\/api\/v1\/?$/, '')
      const res = await fetch(`${root}/readyz`)
      setHealth(res.ok ? '服务就绪（readyz 200）' : `服务未就绪（readyz ${res.status}）`)
    } catch {
      setHealth('服务不可达')
    }
  }

  async function toggleService() {
    if (!bridge) return
    setServiceBusy(true)
    try {
      if (service?.state === 'stopped') setService(await bridge.start())
      else { await bridge.stop(); setService(await bridge.status()) }
    } catch (error) {
      setServiceMessage(error instanceof Error ? error.message : '服务操作失败')
    } finally { setServiceBusy(false) }
  }

  async function exportDiagnostics() {
    if (!bridge || !destination.trim()) return
    setServiceBusy(true)
    try {
      const archive = await bridge.exportDiagnostics(destination)
      setServiceMessage(`诊断包已导出：${archive}`)
    } catch (error) {
      setServiceMessage(error instanceof Error ? error.message : '导出失败')
    } finally { setServiceBusy(false) }
  }

  function resolveClear(confirmed: boolean) {
    setConfirm(null)
    if (!confirmed) return
    try {
      window.localStorage.removeItem('aicouncil.taskIndex.v1')
      window.localStorage.removeItem('aicouncil.draft.newTask')
    } catch { /* ignore */ }
    setIndexCount(0)
    showToast({message: '本机任务索引与草稿已清空。'})
  }

  return (
    <div className="page-stack">
      <div className="page-head">
        <div>
          <h1>设置</h1>
          <p className="page-sub">外观与本机数据。设置只保存在当前浏览器/桌面外壳中。</p>
        </div>
      </div>

      <section className="card" aria-labelledby="appearance-title">
        <h2 id="appearance-title">外观</h2>
        <div className="form-grid">
          <RadioRow label="主题" value={theme} onChange={setTheme}
            options={[['dark', '深色'], ['light', '浅色'], ['system', '跟随系统']] as const} />
          <RadioRow label="密度" value={density} onChange={setDensity}
            options={[['standard', '标准'], ['compact', '紧凑']] as const} />
          <RadioRow label="Diff 视图" value={diffView} onChange={setDiffView}
            options={[['unified', '统一'], ['side-by-side', '双列']] as const} />
        </div>
        <p className="settings-note">主题与密度立即生效；Diff 视图影响任务详情中每个文件的差异呈现。</p>
      </section>

      <section className="card" aria-labelledby="service-title">
        <h2 id="service-title">服务状态</h2>
        {bridge ? (
          <>
            <p>本地服务：{service?.state ?? '正在读取'}{service?.workspace ? ` · 工作区：${service.workspace}` : ''}</p>
            {service?.last_error && <p role="alert">{service.last_error}</p>}
            <div className="form-actions">
              <button onClick={toggleService} disabled={serviceBusy}>{service?.state === 'stopped' ? '启动服务' : '安全停止服务'}</button>
              <button className="secondary" onClick={() => { bridge.status().then(setService).catch(() => {}) }} disabled={serviceBusy}>刷新状态</button>
            </div>
            <label>诊断导出目录<input value={destination} onChange={event => setDestination(event.target.value)} placeholder="例如 C:\\Users\\you\\Downloads" /></label>
            <div className="form-actions">
              <button className="secondary" onClick={exportDiagnostics} disabled={serviceBusy || !destination.trim()}>导出脱敏诊断包</button>
            </div>
            {serviceMessage && <p role="status">{serviceMessage}</p>}
          </>
        ) : (
          <>
            <p className="muted">浏览器模式下通过服务就绪探针检查后端状态。</p>
            <div className="form-actions">
              <button className="secondary" onClick={checkHealth}>检查服务状态</button>
              {health && <span role="status">{health}</span>}
            </div>
          </>
        )}
      </section>

      <section className="card" aria-labelledby="data-title">
        <h2 id="data-title">本机数据</h2>
        <p className="muted">任务索引保存了 {indexCount} 个任务 ID（服务端没有任务列表接口，指挥中心与任务列表靠它汇总）。清空后，已有任务仍可通过 ID 直接打开。</p>
        <div className="form-actions">
          <button className="danger" onClick={() => setConfirm({
            title: '清空本机任务索引与草稿？',
            description: <p>只删除保存在本机的索引与未提交草稿，不会删除服务端任务。</p>,
            confirmLabel: '清空',
          })}>清空索引与草稿</button>
        </div>
      </section>

      <section className="card" aria-labelledby="about-title">
        <h2 id="about-title">关于</h2>
        <p className="muted">AI Council Web 控制台：多模型协商、人工批准与受控执行。界面按 DESIGN v2（Modern Web Guidance）实现：原生 dialog/popover、容器查询、oklch + light-dark() 双色板。</p>
      </section>

      <ConfirmDialog request={confirm} onResolve={resolveClear} />
    </div>
  )
}
