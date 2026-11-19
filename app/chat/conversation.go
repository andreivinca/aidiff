package chat

// Conversation is what a backend shows of its conversation at one moment.
// A backend builds a new one for every change and leaves it alone once
// shown.
type Conversation struct {
	Title   string     // replaces the pane's title when not empty
	State   AgentState // what the agent is doing, for the header
	Running []string   // the turns still going, newest first; the stop dialog interrupts one
	Entries []Entry    // the conversation, oldest first, without the queued messages
	Queued  []Queued   // the messages waiting for their turn, in order
	// Delivered names the messages sent from the pane that the backend
	// took over: shown among Entries, or done with. Until then the pane
	// shows them as cards above the composer.
	Delivered []string
	Activity  []Activity // the agent's background work, most recent first
	Settings  Settings
	// Secret is the secret the agent waits for, nil when it waits for none;
	// the composer takes it instead of a message (secret.go).
	Secret *SecretRequest
}

// AgentState is what the agent is doing, in a few words. A backend may
// report any other state, such as how its last turn ended; it is shown as
// is.
type AgentState string

// The states the pane knows.
const (
	AgentIdle    AgentState = "idle"
	AgentRunning AgentState = "running"
	AgentWaiting AgentState = "waiting for you" // on an approval or an answer
)

// State is where a step, a todo or a piece of background work stands.
type State int

// The states of work.
const (
	Done    State = iota // finished, or nothing going on
	Pending              // about to start
	Running
	Waiting // started, waiting on something
	Failed
)

// going reports work that has not finished.
func (s State) going() bool {
	return s == Pending || s == Running || s == Waiting
}

// Entry is one entry of the conversation: UserMessage, AgentMessage, Plan,
// TodoList, Approval, Questions, Failure or Step.
type Entry interface {
	entry()
}

// UserMessage is what the user said.
type UserMessage struct {
	Text string
}

// AgentMessage is the agent's prose, in markdown.
type AgentMessage struct {
	Markdown  string
	Streaming bool // still being written
}

// Plan is a plan the agent proposes, in markdown.
type Plan struct {
	Markdown string
}

// TodoList is the agent's list of steps.
type TodoList struct {
	Steps []TodoStep
}

// TodoStep is one step of a TodoList.
type TodoStep struct {
	Text  string
	State State
}

// Approval is the agent asking to do something. /accept, /decline and
// /cancel answer the newest pending one.
type Approval struct {
	ID      string // the prompt's id, as Decide takes it
	Prompt  string
	Reason  string
	Pending bool // still waiting for a decision
}

// Questions are the agent's questions in one prompt, answered one by one
// with /answer or /1…/9 and sent together.
type Questions struct {
	ID        string // the prompt's id, as Answer and Dismiss take it
	Questions []Question
	Pending   bool // still waiting for the answers
}

// Question is one question of a Questions prompt.
type Question struct {
	Header   string
	ID       string
	Text     string
	Options  []Option // none for a question answered in the user's words
	Multiple bool     // several options may be picked
	OwnWords bool     // the user may answer in their own words although there are options
}

// Option is one choice of a Question.
type Option struct {
	Label       string
	Description string
	Value       string // what picking it answers
}

// Failure is an error the agent ran into.
type Failure struct {
	Text string
}

// Step is one piece of the agent's work: a command, an edit, a thought…
// A run of steps is summed up in one line until /work shows them one by
// one.
type Step struct {
	Kind  StepKind
	Text  string // one line: the command, the file and its line counts, a thought while it goes on
	State State
	Why   string // a failed step's short reason, such as "exit 1"
}

// StepKind is what sort of work a Step is.
type StepKind int

// The kinds of step.
const (
	StepOther      StepKind = iota // anything else; Text names it
	StepCommand                    // a shell command
	StepEdit                       // a file change
	StepThought                    // reasoning, whose text is shown only while it goes on
	StepSearch                     // a search of the files
	StepWeb                        // a search of the web
	StepTool                       // a tool call
	StepSubagent                   // work handed to a subagent
	StepNotice                     // a notice from the system
	StepInterrupt                  // the turn being stopped
	StepCompaction                 // the context compacted
)

func (UserMessage) entry() {
}

func (AgentMessage) entry() {
}

func (Plan) entry() {
}

func (TodoList) entry() {
}

func (Approval) entry() {
}

func (Questions) entry() {
}

func (Failure) entry() {
}

func (Step) entry() {
}

func (SecretRequest) entry() {
}

// Queued is a message waiting for its turn.
type Queued struct {
	ID     string // the message's id; a card of the pane's own with this id gives way to it
	Text   string
	Images int // the images it carries, which taking it back cannot return to the input
	// Steer is a message sent into the running turn that the agent has not
	// read yet; it cannot be taken back.
	Steer bool
}

// Activity is a piece of the agent's background work: a subagent, a
// process, a task.
type Activity struct {
	ID       string // stable across conversations, so the selection follows it
	Agent    bool   // a subagent, listed apart from processes and tasks
	Title    string
	State    State
	Status   string // the state in a word as the list shows it, such as "running"
	Progress string // the latest progress, shown under the title
	Details  []string
}

// active reports work still going: counted in the status line and listed
// under Active.
func (a Activity) active() bool {
	return a.State.going()
}

// Settings is how the agent runs, for the composer's settings line.
type Settings struct {
	Mode      string   // what the agent does with a message, such as "Build"
	Attention bool     // the mode is drawn in the warning color, and so is the composer's rail
	Model     string   // the model, with its effort when it has one
	Details   []string // more, by importance; the last go first as the line runs out of room
}
