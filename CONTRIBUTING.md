# Contributing to virt-platform-autopilot

Thank you for your interest in contributing to the virt-platform-autopilot project!

## Getting Started

The virt-platform-autopilot is a template-driven platform operator that manages OpenShift Virtualization infrastructure automatically. Most contributions involve adding new managed resources (assets) or improving existing ones.

## How to Contribute

### Adding New Platform Components

The most common contribution is adding new assets to extend the platform's capabilities. See the **[Adding Assets Guide](docs/adding-assets.md)** for step-by-step instructions on:

- Creating template files
- Adding entries to the metadata catalog
- Testing your assets
- Handling soft dependencies
- Generating RBAC permissions

### Development Workflow

1. **Fork the repository** and create a feature branch
2. **Make your changes** following the patterns in the codebase
3. **Test your changes**:
   ```bash
   make test              # Run unit tests
   make test-integration  # Run integration tests
   make kind-setup        # Setup local cluster
   make deploy-local      # Deploy and test locally
   ```
4. **Format your code**: `make fmt`
5. **Submit a pull request** with a clear description of your changes

### Local Development

For setting up a local development environment with Kind (Kubernetes in Docker), see the **[Local Development Guide](docs/local-development.md)**.

Quick start:
```bash
make kind-setup        # Setup local cluster with CRDs
make deploy-local      # Deploy autopilot
make logs-local        # View logs
make redeploy-local    # Redeploy after changes
```

## Code Guidelines

### Go Code

- Follow standard Go formatting (`gofmt`, `goimports`)
- Add unit tests for new functionality
- Keep functions focused and well-named
- Document exported functions and types

### Templates

- Use `.yaml.tpl` extension for Go templates
- Use `.yaml` for static resources
- Handle missing CRDs gracefully with `crdExists` checks
- Make templates idempotent (same input = same output)
- Add comments for complex template logic

### Documentation

- Update relevant documentation when adding features
- Keep examples current and working
- Use clear, concise language
- Include code examples where helpful

## Project Structure

```
virt-platform-autopilot/
├── cmd/                  # Entrypoints and CLI tools
├── pkg/                  # Core implementation
│   ├── controller/       # Main reconciler
│   ├── engine/           # Rendering and patching
│   ├── assets/           # Asset loading
│   └── overrides/        # User customization logic
├── assets/               # Embedded templates
│   ├── active/           # Applied to cluster
│   └── tombstones/       # Marked for deletion
├── config/               # Kubernetes manifests
└── docs/                 # Documentation
```

## Testing

All contributions should include appropriate tests:

### Unit Tests

```bash
make test
```

Test individual functions and logic. Located in `*_test.go` files alongside source code.

### Integration Tests

```bash
make test-integration
```

Test full controller behavior using envtest (simulated API server).

### Local Cluster Testing

```bash
make kind-setup
make deploy-local
kubectl get pods -n openshift-cnv
```

Test with real Kubernetes cluster (Kind).

## Pull Request Process

1. **Ensure all tests pass** before submitting
2. **Update documentation** if adding features or changing behavior
3. **Keep PRs focused** - one feature or fix per PR
4. **Write clear commit messages** describing what and why
5. **Respond to review feedback** promptly

### Commit Messages

Follow these guidelines:
- Use imperative mood ("Add feature" not "Added feature")
- Keep first line under 72 characters
- Include details in body if needed
- Reference issues: "Fixes #123"

Example:
```
Add GPU passthrough asset for NVIDIA devices

Implements automatic GPU passthrough configuration when NVIDIA
devices are detected. Uses soft dependencies to gracefully handle
clusters without GPU operator installed.

Fixes #42
```

## Getting Help

- **Documentation**: Start with [README.md](README.md) and [docs/](docs/)
- **Issues**: Check existing issues or open a new one
- **Architecture**: See [ARCHITECTURE.md](docs/ARCHITECTURE.md) for technical details

## Code of Conduct

Be respectful, inclusive, and collaborative. We welcome contributors of all backgrounds and skill levels.

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.

---

**Ready to add your first asset?** Start with the **[Adding Assets Guide](docs/adding-assets.md)**!
