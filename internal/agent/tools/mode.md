Switches between specialized context modes.

<usage>
- Use `action: "list"` to see available modes
- Use `action: "activate"` with `name` to enter a mode
- Use `action: "deactivate"` to exit the current mode
- Use `action: "status"` to see the current active mode
</usage>

<when_to_use>
- User explicitly asks to switch modes ("switch to infra mode", "enter k8s mode")
- User mentions they're about to work on a specific domain that has a mode
- User asks what modes are available
</when_to_use>

<modes_explained>
Modes pre-load relevant context for specific types of work:
- Tagged memories related to the domain
- Context documents (topology, architecture, etc.)
- Mode-specific instructions and guidelines

This helps you have immediate context without re-learning the same things each session.
</modes_explained>
