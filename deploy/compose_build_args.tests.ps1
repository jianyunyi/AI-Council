$repositoryRoot = Split-Path -Parent $PSScriptRoot

Describe 'production build network configuration' {
    foreach ($dockerfile in 'Dockerfile.council', 'Dockerfile.runner') {
        It "$dockerfile accepts a configurable Go module proxy" {
            $content = Get-Content -Raw (Join-Path $PSScriptRoot $dockerfile)

            $content | Should Match '(?m)^ARG GOPROXY=https://goproxy\.cn,direct$'
            $content | Should Match '(?m)^ENV GOPROXY=\$GOPROXY$'
        }
    }

    It 'passes the configured Go module proxy to both Go image builds' {
        $compose = Get-Content -Raw (Join-Path $PSScriptRoot 'docker-compose.yml')

        foreach ($service in 'council', 'runner') {
            $pattern = "(?ms)^  ${service}:\s+build:.*?args:\s+GOPROXY: \$\{GOPROXY:-https://goproxy\.cn,direct\}"
            $compose | Should Match $pattern
        }
    }

    It 'validates Compose without printing interpolated environment values' {
        $validator = Get-Content -Raw (Join-Path $PSScriptRoot 'validate-compose.ps1')

        $validator | Should Match '(?m)config --quiet$'
    }
}
