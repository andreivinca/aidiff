// Package chat is a chat pane for a coding agent: the conversation, a
// composer with slash commands and attachments (images and files), the
// agent's prompts
// (approvals and questions) and the work it runs in the background.
//
// The pane knows no agent system. A Backend shows it the conversation and
// carries out what the user asks; the backend's own commands join the
// pane's slash commands. Pane implements the split's Terminal contract
// structurally, so it sits in a column of the split the way a shell does.
package chat

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
)

// ErrStopped is wrapped by an Interrupt error whose stop went through.
var ErrStopped = errors.New("stopped")

// Backend is the agent system behind a pane. The pane calls Subscribe off
// its lock, and Send, Interrupt, Decide, Answer and Dismiss too, one at a
// time; what they report once the pane follows another backend is dropped.
// It calls Commands and CheckImage, and a command's Run and Complete, with
// its lock held: they must not block, and may show the conversation (see
// Viewer). CheckImage is also called off the lock, before a file is read.
type Backend interface {
	// Subscribe shows the conversation to v until ctx ends or the backend
	// loses it; the error says why it was lost.
	Subscribe(ctx context.Context, v Viewer) error
	// Send hands the user's message to the agent.
	Send(ctx context.Context, m Outgoing) error
	// Interrupt stops the running turn named turnID, and lets the next
	// queued message start. An error that wraps ErrStopped means the stop
	// went through and only what came after it failed.
	Interrupt(ctx context.Context, turnID string) error
	// Decide answers the approval prompt promptID.
	Decide(ctx context.Context, promptID string, d Decision) error
	// Answer replies to every question of the prompt promptID, by question
	// id.
	Answer(ctx context.Context, promptID string, answers map[string]Answer) error
	// Dismiss drops the questions of the prompt promptID unanswered.
	Dismiss(ctx context.Context, promptID string) error
	// Commands are the backend's slash commands, its own first, then the
	// agent's.
	Commands() []Command
	// CheckImage turns down an image the backend cannot take, before it
	// is read when it comes from a file. A backend that is a FileTaker
	// may still take it as a file.
	CheckImage(name string, size int64) error
}

// FileTaker is a backend that takes files of any type besides images. A
// pane attaches only images to the messages of a backend that is not one.
type FileTaker interface {
	// CheckAttachments turns down attachments one message cannot carry
	// together, the newest last: one too large or empty, too many, too much
	// in all. Its error says why the newest cannot join. Called with the
	// pane's lock held: it must not block.
	CheckAttachments(atts []Attachment) error
}

// Unqueuer is a backend that can take a queued message back before it
// starts, for the user to edit; ↑ on an empty input does it.
type Unqueuer interface {
	// Unqueue drops the queued message messageID from the queue.
	Unqueue(ctx context.Context, messageID string) error
}

// Viewer is where a backend's subscription reports. Its methods never
// block: a backend may call them from any goroutine, with its own locks
// held.
type Viewer interface {
	// Show replaces what the pane shows of the conversation.
	Show(c Conversation)
	// Note tells the user something under the composer until the next key.
	Note(text string)
}

// Outgoing is a message the user sends.
type Outgoing struct {
	ID     string // minted by the pane, a random UUID; later conversations name the message by it
	Text   string // as typed, with the tokens that stand for its images and files
	Images []Image
	Files  []File
	// Progress, when set, is told how far the files went while they are
	// sent: the file's name, the bytes sent of it and its size. It never
	// blocks, and may be called from any goroutine.
	Progress func(name string, sent, size int64)
}

// attachmentToken matches the tokens images and files leave in the input:
// "[Image #1]", "[File #1: report.pdf]".
var attachmentToken = regexp.MustCompile(`\[(?:Image #\d+|File #\d+(?:: [^\]\n]*)?)\]`)

// Prose is the message's text without the tokens of its attachments.
func (m Outgoing) Prose() string {
	return strings.TrimSpace(attachmentToken.ReplaceAllString(m.Text, ""))
}

// Image is an image attached to a message, read when it was attached.
type Image struct {
	Name string
	MIME string
	Data []byte
}

// File is a file attached to a message. It is read as the message is sent,
// so it is not held in memory meanwhile, and goes as it is by then.
type File struct {
	Name string // its base name
	MIME string // its media type
	Size int64  // its size when it was attached
	// Open opens it to send it: its content, no more than its size now,
	// and that size. It fails when the file is gone or no longer one that
	// can be sent.
	Open func() (io.ReadCloser, int64, error)
}

// Attachment is something a message carries, as the transcript lists it
// and FileTaker.CheckAttachments weighs it.
type Attachment struct {
	Kind string // AttachedImage, AttachedFile, or a kind of the backend's own
	Name string
	MIME string
	Size int64
}

// The kinds of attachment the pane sends.
const (
	AttachedImage = "image" // the agent sees it as an image
	AttachedFile  = "file"  // the agent gets the file to read
)

// Decision answers an approval prompt.
type Decision string

// The decisions an approval prompt takes.
const (
	Accept  Decision = "accept"
	Decline Decision = "decline"
	Cancel  Decision = "cancel"
)

// Answer is the reply to one question: the values of the options picked,
// or the user's own words as the one value.
type Answer struct {
	Values   []string
	Multiple bool // the question takes several options, so it is answered with a list
}

// Command is a slash command a backend adds to the pane's own. A command
// of the pane's own wins over a backend's of the same name.
type Command struct {
	Name        string // with its slash: "/model"
	Aliases     []string
	Args        string // what it takes, as the popup shows it: "<model>"; "" when nothing
	OptionalArg bool   // the bare command runs on enter too, although it takes an argument
	Description string
	// Run carries the command out for what was typed after its name. An
	// error turns the input down: it stays in the composer and the note
	// says why. A nil Run marks the agent's own command, which goes to the
	// agent as a message, and is listed after the pane's and the backend's.
	Run func(arg string) (Reply, error)
	// Complete lists what the argument could be, for what was typed of it;
	// nil when the command takes none or any text.
	Complete func(prefix string) []Suggestion
}

// Reply is what a command did, or has left to do.
type Reply struct {
	// Note is shown under the composer: at once, or with Send once it
	// succeeded.
	Note string
	// Send is the part that waits on the backend; the composer waits on it
	// as on a message. Its error says what failed. Nil when the command is
	// done.
	Send func(ctx context.Context) error
}

// Suggestion is one row of the slash-command popup.
type Suggestion struct {
	Insert      string // the input once completed; "" for a row that cannot be picked, such as a problem
	Label       string
	Description string
	Runs        bool // enter runs the completed input at once
}
