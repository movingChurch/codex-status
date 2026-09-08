# Contributing

Use Python 3.14 on macOS with tmux and the Apple Command Line Tools. Build with `python3 src/build-client.py`, then run `python3 -m unittest discover -s tests -v` from `src/`.

Keep the launcher, collector, binding, event parser, and renderer responsibilities separate. Changes to session matching must reject ambiguous fixtures. Add behavioral tests for new data contracts and lifecycle changes. Use synthetic records; never commit real rollout logs, credentials, or user paths.

Describe what changed and how you checked it. Performance claims need a reproducible workload and a clear process scope. CLI version support requires source and fixture evidence; changing the version string alone is not enough.

Use GitHub issues for reproducible bugs and feature proposals. Include versions and expected versus actual behavior, with sensitive data removed. Pull requests are welcome. Contributions use the repository license.
