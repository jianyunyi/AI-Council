/** @type {import('next').NextConfig} */
// pnpm's symlinked node_modules cannot be copied into Next standalone output
// on Windows without developer-mode privileges. Keep standalone for Linux
// containers while using the regular build output on Windows.
const nextConfig = process.env.DESKTOP_BUILD === '1'
  ? { output: 'export', images: { unoptimized: true } }
  : process.platform === 'win32' ? {} : { output: 'standalone' }
export default nextConfig
