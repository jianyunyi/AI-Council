import GroupSpace from '@/components/groups/GroupSpace'

// Static-export builds (desktop) need one placeholder param; the real group
// id arrives via the path at runtime. Same pattern as app/tasks/[id].
export function generateStaticParams() {
  return [{ id: 'desktop' }]
}

export default async function GroupPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params
  return <GroupSpace id={id} />
}
