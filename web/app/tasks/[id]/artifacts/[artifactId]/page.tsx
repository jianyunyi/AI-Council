import {ArtifactReview} from './review'

export function generateStaticParams() {
  return [{id: 'desktop', artifactId: 'desktop'}]
}

export default function ArtifactReviewPage({params}: {params: {id: string; artifactId: string}}) {
  return <ArtifactReview params={params} />
}
