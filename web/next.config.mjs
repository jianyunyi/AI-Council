/** @type {import('next').NextConfig} */
// pnpm's symlinked node_modules cannot be copied into Next standalone output
// on Windows without developer-mode privileges. Keep standalone for Linux
// containers while using the regular build output on Windows.
const baseConfig = process.env.DESKTOP_BUILD === '1'
  ? { output: 'export', images: { unoptimized: true } }
  : process.platform === 'win32' ? {} : { output: 'standalone' }
const apiOrigin = process.env.E2E_API_ORIGIN?.replace(/\/$/, '')
const nextConfig = apiOrigin ? {
  ...baseConfig,
  async rewrites() {
    return [{source: '/api/v1/:path*', destination: `${apiOrigin}/api/v1/:path*`}]
  },
} : baseConfig
export default nextConfig
