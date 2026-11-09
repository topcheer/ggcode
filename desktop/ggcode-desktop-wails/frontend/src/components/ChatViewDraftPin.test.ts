// #5308 钉测:ChatView 草稿跨会话污染守卫必须存在且顺序正确
// 行为面(切换会话后保存 effect 首跑跳过写入、恢复 effect 落归属)由
// draftSessionRef 的三处源级不变量钉住;完整行为验证需 Layout 测试座
// 全 harness,不在本钉测范围。
import { describe, it, expect } from 'vitest'

// vite 原生 raw 导入(项目 tsconfig 无 node 类型,node:* 全拒)——
// vitest 经 vite transform 管线支持 glob+raw
const modules = import.meta.glob('./ChatView.tsx', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>
const src = modules['./ChatView.tsx'] ?? ''

describe('#5308 draft session guard', () => {
  it('save effect must guard against stale-session write on switch', () => {
    expect(src).toContain('draftSessionRef.current !== sessionId')
  })

  it('guard must precede the draft write in the save effect', () => {
    const guard = src.indexOf('draftSessionRef.current !== sessionId')
    const write = src.indexOf('sessionStorage.setItem(`draft:')
    expect(guard).toBeGreaterThan(-1)
    expect(write).toBeGreaterThan(guard)
  })

  it('ownership must be established in both the skip branch and after restore', () => {
    const hits = src.split('draftSessionRef.current = sessionId').length - 1
    expect(hits).toBeGreaterThanOrEqual(2)
  })
})
