## **Liquid Framework (`go-ng`)**

## **High-Performance Server-Driven UI Framework with Binary State Mirror Hydration**

Optimized for Native Performance, Systems-Level Concurrency, and Autonomous AI Agent UI Orchestration.

---

## **🤖 Agentic Framework Abstract (Read First)**

Liquid is a server-driven UI engine built in Go. It uses an Angular-style template model (`.lsx`) that translates layouts into native Go code before rendering them.

Unlike JavaScript web frameworks (React, Angular, Next.js), Liquid **removes all state management and heavy framework bundles from the client browser.** The client runs zero custom runtime JavaScript. Instead, interactive components are mapped to physical memory pointers inside the server's RAM.

## **🧠 Why Liquid is Optimized for Agentic Code Generation**

1. **Simplified Single-Struct Scope**: To modify a view or handle user input, an AI agent does not need to edit multiple files or manage state hooks, side-effects (`useEffect`), or complex browser APIs. You only need to add variables or methods to a single **Go Struct** and place a matching variable tag in the HTML template.
2. **Safe Compilation Sandboxing**: If an agent generates broken syntax or incorrect template directives, Liquid's Ahead-of-Time (AOT) compiler tool flags the issue *on the server* before the application runs. This prevents your runtime web server from crashing and passes compilation logs directly back to the agent for automated self-healing.
3. **No CORS or API Token Handling**: Because generated components execute entirely within your secure backend network infrastructure, buttons and event hooks access databases or internal microservices directly. The agent does not need to configure authorization headers or set up client-side network fetch operations.

---

## **🏗️ System Architecture Core Topology**

```
       [ USER / BROWSER INTERFACE ]
                    │
      ( Interactive Event Callback )
                    │
                    ▼
     [ SECURE HTTPS/WS GATEWAY ] ───► Enforces CSRF Token Validation
                    │
                    ▼
    [ LIQUID ROUTER RE-ANCHOR LOOP ]
                    │
  1. Resolves Token Key ──► Maps to 64-bit Struct Pointer (0x7f9a12c8b000)
  2. Reflective Invocation ─► Calls Requested Method Struct Pointer Target
  3. Parallel Execution ───► Spawns Goroutines to Fetch API Data Sources
  4. AOT Template Update ──► Compiles .lsx Diff Into Standard Go Arrays
                    │
                    ▼
       [ STREAM MICRO-DOM HTML PATCH ]
```

---

## **🛠️ Directives & Syntactic Templates Reference Map**

Liquid converts customized `.lsx` elements into native, thread-safe Go code arrays using an internal string processing engine. Agents must output template markup using these exact syntax markers:

- **Property Interpolation**: `{{ FieldName }}` maps variables to matching struct properties.
- **Loop Variable Scope**: `{{ $variable }}` references values created inside a dynamic loop tag context.
- **Structural Evaluation Directive (`goIf`)**: `<div *goIf="Condition"></div>` compiles to `{{if .Condition}}`.
- **Collection Looping Directive (`goFor`)**: `<li *goFor="let target of List">{{ $target }}</li>` compiles to `{{range $target := .List}}`.
- **Element Interactive Listeners (`(click)`)**: `<button (click)="MethodName">Text</button>` binds browser interaction events directly to server-side struct methods via reflection.
- **Identity Tracking Attributes (`[hydroId]`)**: Attach this token attribute to your root element container to let Liquid track and synchronize component memory addresses: `<div [hydroId] id="surface">`.
- **Localized Translation String Attributes (`goTranslate`)**: `<h2 *goTranslate="DICTIONARY_KEY"></h2>`injects localized translations directly into element tags on the server before streaming pages down the network.

---

## **💻 Standard Structural Blueprint Layout**

Agents must output application structures following this unified Go component layout model:

```go
package components 
import "://github.com" 

// AccountStatusComponent maps view layouts to backend memory addresses.type AccountStatusComponent 

struct { 
// Injected automatically by the Liquid system registry engine
	HydroID     string
	CSRFToken   string 
	// Component State Properties (Mapped to your template)
	NodeID      string
	IsActive    bool
	ClusterLogs []string
} 
// Selector sets the custom HTML element tag identifier. 

func (c *AccountStatusComponent) Selector() string {return"app-account-status"
} 
// Template returns your presentation markup code.// Note: This layout text block can also be written in a separate components/account-status.lsx file. 

func (c *AccountStatusComponent) Template() string { 
return`
	<div [hydroId] id="render-target" class="card">
		<h2>Terminal Node ID: {{ NodeID }}</h2>

		<div *goIf="IsActive" style="color: green;">
			● Connection Status: Active State
		</div>

		<h3>System Log Buffers:</h3>
		<ul>
			<li *goFor="let log of ClusterLogs">
				Log Reference: {{ $log }}
			</li>
		</ul>

		<button (click)="TriggerHardReboot">Execute System Emergency Reset</button>
	</div>
	`
} 

// NgOnInit manages data initialization logic paths. 
func (c *AccountStatusComponent) NgOnInit() {if c.NodeID =="" {
		c.NodeID ="matrix-edge-770"
		c.IsActive = true
		c.ClusterLogs = []string{"Link active","Cache verification pass"}
	}
} 

// TriggerHardReboot handles element interaction events. 
func (c *AccountStatusComponent) TriggerHardReboot() {
	c.IsActive = false
	c.ClusterLogs = append(c.ClusterLogs,"Immediate System Reboot Triggered via Server RAM Struct Pointer Pointer")
}
```

---

## **⚡ Automated Code Compilation & Deployment Sequence**

If you are generating or editing application views automatically based on client sentiment logs, follow this explicit execution sequence:

1. **Write the Template Structure**: Generate and save the layout markup file to disk using a unique component name slot: `./components/my-generated-view.lsx`.
2. **Run the Code Generator Tool**: Invoke the Liquid Ahead-of-Time compiler utility using the command line:
    
    ```bash
    go run cli/compiler.go
    ```
    
3. **Verify App Health**: Check the build output. If errors are returned, copy the stack details back to your processing prompt loop to fix the code automatically. If the build passes, Liquid updates your active backend server routing table—**instantly activating the new page without requiring a server reboot.**

---

## **📦 Automated Verification Testing Setup**

To verify that your generated components are clean, thread-safe, and ready for production, execute your testing suite across the workspace:

```bash
# Run comprehensive framework unit tests
go test -v ./core/...# Measure rendering speeds and memory allocation footprints
go test -bench=. -benchmem ./core/...
```

Expected operational targets: Component layout rendering times must stay **under 500 microseconds**, with a framework footprint of **0 total bytes** of custom JavaScript sent to the user's browser.