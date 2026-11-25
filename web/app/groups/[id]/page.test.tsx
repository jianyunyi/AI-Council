import { describe, expect, it } from 'vitest'
import GroupPage, { generateStaticParams } from './page'
import GroupSpace from '@/components/groups/GroupSpace'

// The route is a thin server wrapper (desktop static export needs
// generateStaticParams); the interactive body lives in GroupSpace.
describe('group detail route', () => {
  it('exports the desktop placeholder param', () => {
    expect(generateStaticParams()).toEqual([{ id: 'desktop' }])
  })

  it('renders GroupSpace with the route id', async () => {
    const el = await GroupPage({ params: Promise.resolve({ id: 'grp_1' }) })
    expect(el.type).toBe(GroupSpace)
    expect(el.props.id).toBe('grp_1')
  })
})
