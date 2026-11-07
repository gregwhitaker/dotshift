"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowDown, ArrowRight, ArrowUpRight, Check, ChevronDown, Code2, Copy, GitBranch, Github, Menu, Monitor, Play, RotateCcw, ShieldCheck, Terminal, X } from "lucide-react";

const REPO = "https://github.com/gregwhitaker/dotshift";
const DOCS = `${REPO}#readme`;

function Dotmark({ className = "" }: { className?: string }) {
  return <svg className={`dotmark ${className}`} viewBox="0 0 32 32" fill="currentColor" aria-hidden="true"><circle cx="7" cy="25" r="4.5"/><circle cx="7" cy="13" r="4.5"/><circle cx="19" cy="13" r="4.5"/><circle cx="19" cy="25" r="4.5"/><circle cx="25" cy="5" r="3"/></svg>;
}

function DotField() {
  return <svg className="dot-field" viewBox="0 0 540 570" aria-hidden="true">
    {Array.from({ length: 25 }, (_, row) => Array.from({ length: 25 }, (_, col) => {
      const x = 14 + col * 22, y = 12 + row * 22;
      const distance = Math.hypot(x - 320, y - 300);
      return <circle key={`${row}-${col}`} cx={x} cy={y} r={Math.max(0.7, 5 - distance / 100)} fill="currentColor" opacity={distance < 230 ? 0.72 : 0.15}/>;
    }))}
  </svg>;
}

const demoSteps = ["Set a goal", "Run the loop", "Review the work"];

function WorkflowDemo() {
  const [step, setStep] = useState(1);
  const tabs = useRef<(HTMLButtonElement | null)[]>([]);
  function selectStep(next: number, focus = false) {
    setStep(next);
    if (focus) tabs.current[next]?.focus();
  }
  return <div className="hero-visual">
    <div className="visual-caption"><span className="tiny-cross">+</span> A LITTLE LESS BACK-AND-FORTH.</div>
    <DotField/>
    <div className="runner-window">
      <div className="runner-bar"><span className="window-dots"><i/><i/><i/></span><span>dotshift / local runner</span><Terminal size={14}/></div>
      <div className="runner-heading"><Dotmark/><div><strong>One goal. A whole lot of progress.</strong><span>workspace / search</span></div><span className="runner-light"/></div>
      <div id="demo-panel" role="tabpanel" aria-labelledby={`demo-tab-${step}`} className="runner-content" tabIndex={0}>
        {step === 0 && <div className="demo-view" key="goal">
          <div className="console-label">THE HANDOFF</div>
          <div className="goal-message"><span>You</span><p>“Add full-text search to the workspace. Cover the edge cases and keep going until the tests pass.”</p></div>
          <div className="console-label goal-label">DOTSHIFT GETS TO WORK</div>
          <div className="log-line"><Check/><span>Project and test command loaded</span></div>
          <div className="log-line"><Check/><span>Created a separate working branch</span></div>
          <div className="branch-tag"><GitBranch size={14}/> dotshift/workspace-search</div>
        </div>}
        {step === 1 && <div className="demo-view" key="loop">
          <div className="console-label"><span>GOAL IN PROGRESS</span><span className="live-label"><i/> WORKING</span></div>
          <p className="active-goal">Add full-text search to the workspace.</p>
          <div className="task-log">
            <div className="log-line"><Check/><span>Created isolated worktree</span><span className="log-note">done</span></div>
            <div className="log-line"><Check/><span>Implemented search</span><span className="log-note">done</span></div>
            <div className="log-line log-retry"><RotateCcw/><span>Fixed an empty-query edge case</span><span className="log-note">retry</span></div>
            <div className="log-line log-active"><span className="working-dot"/><span>Running the test suite</span><span className="cursor">_</span></div>
          </div>
          <div className="code-snippet"><span className="code-muted">// the loop that follows through</span><br/><span className="code-purple">while</span> (!goal.complete) &#123;<br/>&nbsp;&nbsp;build(); test(); fix();<br/>&#125;</div>
        </div>}
        {step === 2 && <div className="demo-view" key="review">
          <div className="console-label">READY FOR YOUR EYES</div>
          <div className="done-heading"><span><Check size={23}/></span><h3>Goal complete.<br/>Your branch is ready.</h3></div>
          <div className="log-line"><Check/><span>Configured tests passed</span></div>
          <div className="log-line"><ShieldCheck/><span>Completion review passed</span></div>
          <div className="branch-tag"><GitBranch size={14}/> dotshift/workspace-search</div>
          <p className="review-note">You review the diff. You decide what ships.</p>
        </div>}
      </div>
      <div className="runner-footer"><span><Monitor size={13}/> On your machine</span><span>On its own branch <GitBranch size={13}/></span></div>
    </div>
    <div className="demo-controls" role="tablist" aria-label="Explore an example dotshift workflow">
      {demoSteps.map((label, index) => <button key={label} ref={el => { tabs.current[index] = el; }} role="tab" id={`demo-tab-${index}`} aria-controls="demo-panel" aria-selected={step === index} tabIndex={step === index ? 0 : -1} onClick={() => selectStep(index)} onKeyDown={event => {
        if (event.key === "ArrowRight" || event.key === "ArrowLeft") { event.preventDefault(); selectStep((step + (event.key === "ArrowRight" ? 1 : 2)) % 3, true); }
        if (event.key === "Home" || event.key === "End") { event.preventDefault(); selectStep(event.key === "Home" ? 0 : 2, true); }
      }}><span>0{index + 1}</span>{label}</button>)}
    </div>
    <p className="demo-disclaimer">An example workflow. A very real way to work.</p>
    <div className="visual-coordinate" aria-hidden="true">[ LESS BUSYWORK. MORE BUILDING. ]</div>
  </div>;
}

const setupCommands = {
  subscription: `# Build dotshift from source (Go 1.25+)
git clone https://github.com/gregwhitaker/dotshift.git
cd dotshift
go build -o dotshift ./cmd/dotshift

# Sign in with the installed agent CLIs
codex login
claude auth login
./dotshift init -mode subscription

# Add your repos and test commands to dotshift.yaml
./dotshift check
./dotshift serve`,
  api: `# Install dotshift (Go 1.25+)
go install github.com/gregwhitaker/dotshift/cmd/dotshift@latest

# Create your local configuration
dotshift init

# Set a key for your chosen provider
export OPENAI_API_KEY="your-api-key"

# Add your repos and test commands to dotshift.yaml
# Start Docker, then start the service
dotshift serve`,
};

function Setup() {
  const [mode, setMode] = useState<"subscription" | "api">("subscription");
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle");
  const resetTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const tabRefs = useRef<(HTMLButtonElement | null)[]>([]);
  useEffect(() => () => { if (resetTimer.current) clearTimeout(resetTimer.current); }, []);
  async function copy() {
    try { await navigator.clipboard.writeText(setupCommands[mode]); setCopyState("copied"); }
    catch { setCopyState("failed"); }
    if (resetTimer.current) clearTimeout(resetTimer.current);
    resetTimer.current = setTimeout(() => setCopyState("idle"), 3500);
  }
  return <section className="setup-section section-wrap" id="get-started">
    <div className="setup-copy">
      <div className="eyebrow"><span className="section-number">03</span> YOUR NEXT SHIFT STARTS HERE</div>
      <h2>Less setup.<br/><span className="serif-word">More starting.</span></h2>
      <p>Bring a project, pick your agents, and give them something worth building.</p>
      <ol className="setup-steps">
        <li><span>1</span><div><strong>Make yourself at home.</strong><p>Install dotshift on your machine with Go 1.25+.</p></div></li>
        <li><span>2</span><div><strong>Choose how you run.</strong><p>Use your Codex and Claude Code subscriptions, or bring API keys and Docker.</p></div></li>
        <li><span>3</span><div><strong>Give it a place to work.</strong><p>Add a repository and test command. Connect your MCP client to hand off a goal.</p></div></li>
      </ol>
      <a className="text-link" href={`${REPO}#connecting-your-dot-mcp`} target="_blank" rel="noreferrer">Read the full setup guide <ArrowUpRight size={17}/></a>
    </div>
    <div className="setup-terminal">
      <div className="setup-tabs" role="tablist" aria-label="Installation mode">
        {(["subscription", "api"] as const).map((item, index) => <button ref={el => { tabRefs.current[index] = el; }} role="tab" key={item} id={`setup-tab-${item}`} aria-selected={mode === item} aria-controls="setup-panel" tabIndex={mode === item ? 0 : -1} onClick={() => {setMode(item); setCopyState("idle");}} onKeyDown={event => {
          if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) {
            event.preventDefault();
            const next = event.key === "Home" ? 0 : event.key === "End" ? 1 : 1 - index;
            setMode(next === 0 ? "subscription" : "api"); setCopyState("idle"); tabRefs.current[next]?.focus();
          }
        }}>{item === "subscription" ? "Subscriptions" : "API keys"}<span>{item === "subscription" ? "01" : "02"}</span></button>)}
        <button className="copy-button" onClick={copy} aria-label={copyState === "copied" ? "Commands copied" : "Copy setup commands"}>{copyState === "copied" ? <Check size={16}/> : <Copy size={16}/>}</button>
      </div>
      <div role="tabpanel" id="setup-panel" aria-labelledby={`setup-tab-${mode}`} tabIndex={0}>
        <div className="terminal-caption"><span>YOUR TERMINAL</span><span aria-live="polite">{copyState === "copied" ? "Copied to clipboard" : copyState === "failed" ? "Select the commands to copy" : "bash"}</span></div>
        <pre><code>{setupCommands[mode].split("\n").map((line, index) => <span key={index} className={line.startsWith("#") ? "shell-comment" : "shell-command"}>{line || " "}{"\n"}</span>)}</code></pre>
        <div className="setup-note"><span className="small-status-dot"/>{mode === "subscription" ? "Uses your existing CLI sign-ins. Install Codex and Claude Code first." : "API mode runs the LiteLLM router in Docker. Provider usage is billed separately."}</div>
      </div>
    </div>
  </section>;
}

const faqs = [
  { question: "Is dotshift another coding agent?", answer: "dotshift coordinates the agents you already use. It gives Codex or Claude Code a goal, runs your configured tests, feeds failures back into the next attempt, and asks a fresh agent invocation to review completion. It’s the service that keeps the work moving between those steps." },
  { question: "Does everything run on my machine?", answer: "The dotshift service, repositories, working branches, and test commands run on your computer. Your chosen model provider still receives the context its coding agent needs. API mode also supports local Ollama models; compatibility depends on the model, so run dotshift check before handing off a goal." },
  { question: "Can I use my existing subscriptions?", answer: "Yes. Subscription mode runs the official Codex and Claude Code CLIs with their saved sign-ins. It doesn’t silently fall back to API billing. Your provider’s plan limits and extra-usage settings still apply. API mode is a separate option for OpenAI, Anthropic, Gemini, or local models." },
  { question: "What happens when the tests fail?", answer: "The failures go straight into the next coding iteration, along with the original goal and saved progress. You can set iteration limits and per-attempt timeouts. When a goal needs outside input or reaches a limit, it blocks so you can inspect and resume it." },
  { question: "Who decides what gets merged?", answer: "You do. Each goal works in an isolated Git worktree on its own branch. Your working copy stays untouched. When the tests and completion review pass, dotshift leaves the result on that branch for you to review and merge." },
];

export default function Home() {
  const [menuOpen, setMenuOpen] = useState(false);
  const closeMenu = () => setMenuOpen(false);
  return <>
    <a className="skip-link" href="#main">Skip to content</a>
    <header className="site-header">
      <div className="header-inner">
        <a href="#" className="wordmark" aria-label="dotshift home" onClick={closeMenu}><Dotmark/>dotshift<span className="wordmark-period">.</span></a>
        <nav className="desktop-nav" aria-label="Main navigation"><a href="#how-it-works">How it works</a><a href="#why-dotshift">Why dotshift</a><a href="#faq">FAQs</a></nav>
        <div className="header-actions"><a className="github-link" href={REPO} target="_blank" rel="noreferrer" aria-label="dotshift on GitHub"><Github size={19}/><span>GitHub</span><ArrowUpRight size={13}/></a><a className="button button-small button-dark" href="#get-started">Get started <ArrowUpRight size={15}/></a><button className="menu-toggle" aria-label={menuOpen ? "Close menu" : "Open menu"} aria-expanded={menuOpen} aria-controls="mobile-nav" onClick={() => setMenuOpen(!menuOpen)}>{menuOpen ? <X/> : <Menu/>}</button></div>
      </div>
      {menuOpen && <nav id="mobile-nav" className="mobile-nav" aria-label="Mobile navigation" onKeyDown={event => { if(event.key === "Escape") { closeMenu(); document.querySelector<HTMLButtonElement>(".menu-toggle")?.focus(); } }}><a href="#how-it-works" onClick={closeMenu}>How it works <ArrowRight size={16}/></a><a href="#why-dotshift" onClick={closeMenu}>Why dotshift <ArrowRight size={16}/></a><a href="#faq" onClick={closeMenu}>FAQs <ArrowRight size={16}/></a><a href={REPO} target="_blank" rel="noreferrer" onClick={closeMenu}>View on GitHub <ArrowUpRight size={16}/></a></nav>}
    </header>
    <main id="main">
      <section className="hero section-wrap">
        <div className="hero-copy">
          <div className="eyebrow hero-eyebrow"><span className="status-dot"/> AUTONOMOUS CODING. LOCALLY RUN.</div>
          <h1>Big ideas.<br/>Meet follow-<br/><span className="hero-highlight">through.<svg viewBox="0 0 400 18" preserveAspectRatio="none" aria-hidden="true"><path d="M4 12 Q160 0 395 7"/></svg></span></h1>
          <p className="hero-description">Give your coding agents a goal, not another prompt. dotshift builds, tests, and keeps fixing—then hands you a branch to review.</p>
          <div className="hero-actions"><a className="button button-lime" href="#get-started">Put it to work <ArrowUpRight size={19}/></a><a className="button button-quiet" href="#how-it-works"><span className="play-circle"><Play size={11} fill="currentColor"/></span>See how it works</a></div>
          <div className="hero-footnote"><span/><p>Your machine. Your models. Your branch.</p></div>
        </div>
        <WorkflowDemo/>
        <div className="hero-bottom"><span>LESS HAND-HOLDING. MORE HEADWAY.</span><a href="#how-it-works" aria-label="Explore how dotshift works"><ArrowDown size={19}/></a></div>
      </section>
      <div className="compatibility section-wrap"><p>Built around the tools<br/><strong>you already work with.</strong></p><div className="tool-names"><span><Terminal/>Codex</span><span><span className="claude-symbol" aria-hidden="true">✳</span>Claude Code</span><span><Code2/>MCP clients</span><span><GitBranch/>Your Git repos</span></div></div>
      <section className="workflow-section section-wrap" id="how-it-works">
        <div className="section-top"><div><div className="eyebrow"><span className="section-number">01</span> THE FOLLOW-THROUGH IS THE FEATURE</div><h2>A goal goes in.<br/><span className="serif-word">A tested branch comes out.</span></h2></div><p>You bring the direction. dotshift keeps the coding, testing, and fixing moving until the work passes its completion review.</p></div>
        <div className="workflow-grid">
          <article><div className="step-top"><span>01 / HAND IT OFF</span><ArrowUpRight/></div><div className="workflow-glyph goal-glyph" aria-hidden="true"><span>“Build this.”</span><ArrowRight/><Dotmark/></div><h3>Think in goals.</h3><p>Describe what you want through your MCP client. Work in an existing repo, or let dotshift create a new project in your configured workspace.</p></article>
          <article><div className="step-top"><span>02 / LET IT WORK</span><RotateCcw/></div><div className="workflow-glyph loop-glyph" aria-hidden="true"><span>build</span><span>test</span><span>fix</span><svg viewBox="0 0 270 32"><path d="M240 1 V17 Q240 28 227 28 H37 Q24 28 24 17 V1"/><path d="m19 7 5-6 5 6"/></svg></div><h3>Keep the loop moving.</h3><p>Tests fail? It tries again. Review finds a gap? Back to the code. Progress is saved between attempts, with limits you can configure.</p></article>
          <article><div className="step-top"><span>03 / MAKE THE CALL</span><GitBranch/></div><div className="workflow-glyph branch-glyph" aria-hidden="true"><GitBranch/><span>your next feature</span><span className="glyph-check"><Check/></span></div><h3>Review something real.</h3><p>Get a branch with the work, test results, and a completion review. Inspect the diff and decide when it’s ready to merge.</p></article>
        </div>
      </section>
      <section className="ownership-section" id="why-dotshift">
        <div className="section-wrap">
          <div className="section-top"><div><div className="eyebrow"><span className="section-number">02</span> YOUR SETUP. YOUR SAY.</div><h2>More autonomy.<br/><span className="serif-word">Same home field.</span></h2></div><p>Your tools shouldn’t need a whole new way of working. dotshift fits around your repositories, agents, and machine.</p></div>
          <div className="ownership-grid">
            <article><div className="feature-illustration machine-illustration" aria-hidden="true"><div className="machine-frame"><div><Dotmark/><span>localhost</span><span className="small-status-dot"/></div><div className="machine-files"><span>~/your-project</span><span>├ main <span className="muted-code">unchanged</span></span><span>└ dotshift/next-idea <span className="lime-text">●</span></span></div></div><div className="machine-base"/></div><h3>Home is where the code is.</h3><p>Work runs on your computer, in repositories you allow. Every task gets an isolated worktree. Your working copy stays yours.</p><div className="feature-caption"><Monitor size={13}/> LOCAL EXECUTION</div></article>
            <article><div className="feature-illustration models-illustration" aria-hidden="true"><span>Codex</span><span>Claude Code</span><div className="model-connection"><i/><Dotmark/><i/></div><span className="model-provider">OpenAI · Anthropic · Gemini · Ollama</span></div><h3>Pick your kind of clever.</h3><p>Use your existing subscriptions, or route API requests to your preferred models. Configure fallbacks and strategies for the job.</p><div className="feature-caption"><Code2 size={13}/> YOUR AGENTS & MODELS</div></article>
            <article><div className="feature-illustration persistence-illustration" aria-hidden="true"><div><span className="checkpoint-dot"/><span>progress saved</span><Check size={14}/></div><div className="persistence-line"/><div><span className="checkpoint-dot"/><span>service restarted</span><RotateCcw size={14}/></div><div className="persistence-line active"/><div className="checkpoint-active"><span className="checkpoint-dot"/><span>right where you left off</span><ArrowRight size={14}/></div></div><h3>A little less starting over.</h3><p>Goals keep their progress between attempts. Restart the service and active work resumes from its saved phase and worktree.</p><div className="feature-caption"><RotateCcw size={13}/> PERSISTENT PROGRESS</div></article>
          </div>
          <div className="ownership-bottom"><ShieldCheck size={18}/><p>Autonomy with a handbrake.</p><span>Set iteration limits. Cancel a task. Review every branch.</span><a href={`${REPO}#project-goals`} target="_blank" rel="noreferrer">You’re in control <ArrowUpRight size={14}/></a></div>
        </div>
      </section>
      <Setup/>
      <section className="faq-section section-wrap" id="faq"><div className="faq-heading"><div className="eyebrow"><span className="section-number">04</span> A FEW GOOD QUESTIONS</div><h2>Before you<br/><span className="serif-word">clock in.</span></h2><a className="text-link" href={DOCS} target="_blank" rel="noreferrer">Get into the docs <ArrowUpRight size={16}/></a></div><div className="faq-list">{faqs.map(({question, answer}, index) => <details key={question}><summary><span className="faq-number">0{index + 1}</span><span>{question}</span><ChevronDown size={19}/></summary><p>{answer}</p></details>)}</div></section>
      <section className="closing-section"><div className="section-wrap closing-inner"><div><span className="eyebrow">THAT IDEA YOU KEEP PUTTING OFF?</span><h2>Give it a shift.</h2></div><a className="button button-dark" href="#get-started">Let’s get to work <ArrowUpRight size={21}/></a><Dotmark className="closing-mark"/></div></section>
    </main>
    <footer className="site-footer section-wrap"><div className="footer-top"><a href="#" className="wordmark" aria-label="dotshift home"><Dotmark/>dotshift<span className="wordmark-period">.</span></a><p>Good ideas deserve follow-through.</p><div><a href={REPO} target="_blank" rel="noreferrer">GitHub <ArrowUpRight size={13}/></a><a href={DOCS} target="_blank" rel="noreferrer">Documentation <ArrowUpRight size={13}/></a></div></div><div className="footer-bottom"><span>© {new Date().getFullYear()} dotshift</span><span className="footer-domain"><span className="small-status-dot"/> dotshift.dev</span><span>Locally run. Forward thinking.</span></div></footer>
  </>;
}
