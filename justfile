import 'just/generation.just'
import 'just/toolings.just'
import 'just/testing.just'
import 'just/integration.just'

default:
    @just --list

# Run every local verification check.
verify: verify-unit integration
