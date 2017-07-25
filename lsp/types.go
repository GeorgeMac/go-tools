// Package lsp implements the Language Server Protocol.
package lsp

import (
	"encoding/json"
	"net/url"
)

const (
	ParseError           = -32700
	InvalidRequest       = -32600
	MethodNotFound       = -32601
	InvalidParams        = -32602
	InternalError        = -32603
	serverErrorStart     = -32099
	serverErrorEnd       = -32000
	ServerNotInitialized = -32002
	UnknownErrorCode     = -32001

	RequestCancelled = -32800
)

const (
	Error       = 1
	Warning     = 2
	Information = 3
	Hint        = 4
)

type URI url.URL

func (u *URI) MarshalText() ([]byte, error) {
	return []byte((*url.URL)(u).String()), nil
}

func (u *URI) UnmarshalText(b []byte) error {
	l, err := url.Parse(string(b))
	if err != nil {
		return err
	}
	*u = *(*URI)(l)
	return nil
}

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   *URI  `json:"uri"`
	Range Range `json:"range"`
}

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     string `json:"code"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

type Command struct {
	Title     string   `json:"title"`
	Command   string   `json:"command"`
	Arguments []string `json:"arguments"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type TextDocumentEdit struct {
	TextDocument string     `json:"textDocument"`
	Edits        []TextEdit `json:"edits"`
}

type WorkspaceEdit struct {
	// TODO(dh): support `changes`
	DocumentChanges []TextDocumentEdit `json:"documentChanges"`
}

type TextDocumentIdentifier struct {
	URI *URI `json:"uri"`
}

type TextDocumentItem struct {
	URI        *URI   `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type VersionedTextDocumentIdentifier struct {
	TextDocumentIdentifier
	Version int `json:"version"`
}

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type DocumentFilter struct {
	Language string `json:"language"`
	Scheme   string `json:"scheme"`
	Pattern  string `json:"pattern"`
}

type DocumentSelector []DocumentFilter

type Message struct {
	JSONRPC string `json:"jsonrpc"`
}

type NotificationMessage struct {
	Message
	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

type RequestMessage struct {
	Message
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type ResponseMessage struct {
	Message
	ID     int            `json:"id"`
	Result interface{}    `json:"result"`
	Error  *ResponseError `json:"error,omitempty"`
}

type ResponseError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

type InitializeParams struct {
	ProcessID             *int                   `json:"processId"`
	RootPath              *string                `json:"rootPath"`
	RootURI               *URI                   `json:"rootUri"`
	InitializationOptions map[string]interface{} `json:"initializationOptions"`
	Capabilities          ClientCapabilities     `json:"capabilities"`
	Trace                 string                 `json:"trace"`
}

type WorkspaceClientCapabilities struct {
	// The client supports applying batch edits to the workspace by supporting
	// the request 'workspace/applyEdit'
	ApplyEdit bool `json:"applyEdit"`

	// Capabilities specific to `WorkspaceEdit`s
	WorkspaceEdit struct {
		// The client supports versioned document changes in `WorkspaceEdit`s
		DocumentChanges bool `json:"documentChanges"`
	} `json:"workspaceEdit"`

	// Capabilities specific to the `workspace/didChangeConfiguration` notification.
	DidChangeConfiguration struct {
		// Did change configuration notification supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"didChangeConfiguration"`

	// Capabilities specific to the `workspace/didChangeWatchedFiles` notification.
	DidChangeWatchedFiles struct {
		// Did change watched files notification supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"didChangeWatchedFiles"`

	// Capabilities specific to the `workspace/symbol` request.
	Symbol struct {
		// Symbol request supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"symbol"`

	// Capabilities specific to the `workspace/executeCommand` request.
	ExecuteCommand struct {
		// Execute command supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"executeCommand"`
}

// Text document specific client capabilities.
type TextDocumentClientCapabilities struct {
	Synchronization struct {
		// Whether text document synchronization supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`

		// The client supports sending will save notifications.
		WillSave bool `json:"willSave"`

		// The client supports sending a will save request and
		// waits for a response providing text edits which will
		// be applied to the document before it is saved.
		WillSaveWaitUntil bool `json:"willSaveWaitUntil"`

		// The client supports did save notifications.
		DidSave bool `json:"didSave"`
	} `json:"synchronization"`

	// Capabilities specific to the `textDocument/completion`
	Completion struct {
		// Whether completion supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`

		// The client supports the following `CompletionItem` specific
		// capabilities.
		CompletionItem struct {
			// Client supports snippets as insert text.
			//
			// A snippet can define tab stops and placeholders with `$1`, `$2`
			// and `${3:foo}`. `$0` defines the final tab stop, it defaults to
			// the end of the snippet. Placeholders with equal identifiers are linked,
			// that is typing in one will update others too.
			SnippetSupport bool `json:"snippetSupport"`
		} `json:"completionItem"`
	} `json:"completion"`

	// Capabilities specific to the `textDocument/hover`
	Hover struct {
		// Whether hover supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"hover"`

	// Capabilities specific to the `textDocument/signatureHelp`
	SignatureHelp struct {
		// Whether signature help supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"signatureHelp"`

	// Capabilities specific to the `textDocument/references`
	References struct {
		// Whether references supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"references"`

	// Capabilities specific to the `textDocument/documentHighlight`
	DocumentHighlight struct {
		// Whether document highlight supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"documentHighlight"`

	// Capabilities specific to the `textDocument/documentSymbol`
	DocumentSymbol struct {
		// Whether document symbol supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"documentSymbol"`

	// Capabilities specific to the `textDocument/formatting`
	Formatting struct {
		// Whether formatting supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"formatting"`

	// Capabilities specific to the `textDocument/rangeFormatting`
	RangeFormatting struct {
		// Whether range formatting supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"rangeFormatting"`

	// Capabilities specific to the `textDocument/onTypeFormatting`
	OnTypeFormatting struct {
		// Whether on type formatting supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"onTypeFormatting"`

	// Capabilities specific to the `textDocument/definition`
	Definition struct {
		// Whether definition supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"definition"`

	// Capabilities specific to the `textDocument/codeAction`
	CodeAction struct {
		// Whether code action supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"codeAction"`

	// Capabilities specific to the `textDocument/codeLens`
	CodeLens struct {
		// Whether code lens supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"codeLens"`

	// Capabilities specific to the `textDocument/documentLink`
	DocumentLink struct {
		// Whether document link supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"documentLink"`

	// Capabilities specific to the `textDocument/rename`
	Rename struct {
		// Whether rename supports dynamic registration.
		DynamicRegistration bool `json:"dynamicRegistration"`
	} `json:"rename"`
}

type ClientCapabilities struct {
	// Workspace specific client capabilities.
	Workspace WorkspaceClientCapabilities `json:"workspace"`

	// Text document specific client capabilities.
	TextDocument TextDocumentClientCapabilities `json:"textDocument"`

	// Experimental client capabilities.
	Experimental map[string]interface{} `json:"experimental"`
}

type InitializeResult struct {
	// The capabilities the language server provides.
	Capabilities ServerCapabilities `json:"capabilities"`
}

// Defines how the host (editor) should sync document changes to the language server.
const (
	// Documents should not be synced at all.
	SyncNone = 0

	// Documents are synced by always sending the full content
	// of the document.
	SyncFull = 1

	// Documents are synced by sending the full content on open.
	// After that only incremental updates to the document are
	// send.
	SyncIncremental = 2
)

// Completion options.
type CompletionOptions struct {
	// The server provides support to resolve additional
	// information for a completion item.
	ResolveProvider bool `json:"resolveProvider"`

	// The characters that trigger completion automatically.
	TriggerCharacters []string `json:"triggerCharacters"`
}

// Signature help options.
type SignatureHelpOptions struct {
	// The characters that trigger signature help
	// automatically.
	TriggerCharacters []string `json:"triggerCharacters"`
}

// Code Lens options.
type CodeLensOptions struct {
	// Code lens has a resolve provider as well.
	ResolveProvider bool `json:"resolveProvider"`
}

// Format document on type options
type DocumentOnTypeFormattingOptions struct {
	// A character on which formatting should be triggered, like `}`.
	FirstTriggerCharacter string `json:"firstTriggerCharacter"`

	// More trigger characters.
	MoreTriggerCharacter []string `json:"moreTriggerCharacter"`
}

// Document link options
type DocumentLinkOptions struct {
	// Document links have a resolve provider as well.
	ResolveProvider bool `json:"resolveProvider"`
}

// Execute command options.
type ExecuteCommandOptions struct {
	// The commands to be executed on the server
	Commands []string `json:"commands"`
}

// Save options.
type SaveOptions struct {
	// The client is supposed to include the content on save.
	IncludeText bool `json:"includeText"`
}

type TextDocumentSyncOptions struct {
	// Open and close notifications are sent to the server.
	OpenClose bool `json:"openClose"`

	// Change notificatins are sent to the server. See TextDocumentSyncKind.None, TextDocumentSyncKind.Full
	// and TextDocumentSyncKindIncremental.
	Change int `json:"change"`

	// Will save notifications are sent to the server.
	WillSave bool `json:"willSave"`

	// Will save wait until requests are sent to the server.
	WillSaveWaitUntil bool `json:"willSaveWaitUntil"`

	// Save notifications are sent to the server.
	Save SaveOptions `json:"save"`
}

type ServerCapabilities struct {
	// Defines how text documents are synced. Is either a detailed structure defining each notification or
	// for backwards compatibility the TextDocumentSyncKind number.
	// TextDocumentSync TextDocumentSyncOptions `json:"textDocumentSync"`
	// TODO(dh): TextDocumentSyncOptions isn't currently working
	TextDocumentSync int `json:"textDocumentSync"`

	// The server provides hover support.
	HoverProvider bool `json:"hoverProvider"`

	// The server provides completion support.
	CompletionProvider CompletionOptions `json:"completionProvider"`

	// The server provides signature help support.
	SignatureHelpProvider SignatureHelpOptions `json:"signatureHelpProvider"`

	// The server provides goto definition support.
	DefinitionProvider bool `json:"definitionProvider"`

	// The server provides find references support.
	ReferencesProvider bool `json:"referencesProvider"`

	// The server provides document highlight support.
	DocumentHighlightProvider bool `json:"documentHighlightProvider"`

	// The server provides document symbol support.
	DocumentSymbolProvider bool `json:"documentSymbolProvider"`

	// The server provides workspace symbol support.
	WorkspaceSymbolProvider bool `json:"workspaceSymbolProvider"`

	// The server provides code actions.
	CodeActionProvider bool `json:"codeActionProvider"`

	// The server provides code lens.
	CodeLensProvider CodeLensOptions `json:"codeLensProvider"`

	// The server provides document formatting.
	DocumentFormattingProvider bool `json:"documentFormattingProvider"`

	// The server provides document range formatting.
	DocumentRangeFormattingProvider bool `json:"documentRangeFormattingProvider"`

	// The server provides document formatting on typing.
	DocumentOnTypeFormattingProvider DocumentOnTypeFormattingOptions `json:"documentOnTypeFormattingProvider"`

	// The server provides rename support.
	RenameProvider bool `json:"renameProvider"`

	// The server provides document link support.
	DocumentLinkProvider DocumentLinkOptions `json:"documentLinkProvider"`

	// The server provides execute command support.
	ExecuteCommandProvider ExecuteCommandOptions `json:"executeCommandProvider"`

	// Experimental server capabilities.
	Experimental map[string]interface{} `json:"experimental,omitempty"`
}

type ShowMessageParams struct {
	// The message type. See {@link MessageType}.
	Type int `json:"type"`

	// The actual message.
	Message string `json:"message"`
}

const (
	// An error message.
	ErrorMessage = 1

	// A warning message.
	WarningMessage = 2

	// An information message.
	InfoMessage = 3

	// A log message.
	LogMessage = 4
)

type ShowMessageRequestParams struct {
	// The message type. See {@link MessageType}
	Type int `json:"type"`

	// The actual message
	Message string `json:"message"`

	// The message action items to present.
	Actions []MessageActionItem `json:"actions"`
}

type MessageActionItem struct {
	// A short title like 'Retry', 'Open Log' etc.
	Title string `json:"title"`
}

type LogMessageParams struct {
	// The message type. See {@link MessageType}
	Type int `json:"type"`

	// The actual message
	Message string `json:"message"`
}

// General paramters to register for a capability.
type Registration struct {
	// The id used to register the request. The id can be used to deregister
	// the request again.
	ID string `json:"id"`

	// The method / capability to register for.
	Method string `json:"method"`

	// Options necessary for the registration.
	RegisterOptions map[string]interface{} `json:"registerOptions"`
}

type RegistrationParams struct {
	Registrations []Registration `json:"registrations"`
}

type TextDocumentRegistrationOptions struct {
	// A document selector to identify the scope of the registration. If set to null
	// the document selector provided on the client side will be used.
	DocumentSelector *DocumentSelector `json:"documentSelector"`
}

// General parameters to unregister a capability.
type Unregistration struct {
	// The id used to unregister the request or notification. Usually an id
	// provided during the register request.
	ID string `json:"id"`

	// The method / capability to unregister for.
	Method string `json:"method"`
}

type UnregistrationParams struct {
	Unregisterations []Unregistration `json:"unregistrations"`
}

type DidChangeConfigurationParams struct {
	// The actual changed settings
	Settings map[string]interface{} `json:"settings"`
}

type DidOpenTextDocumentParams struct {
	// The document that was opened.
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeTextDocumentParams struct {
	// The document that did change. The version number points
	// to the version after all provided content changes have
	// been applied.
	TextDocument VersionedTextDocumentIdentifier `json:"textDocument"`

	// The actual content changes.
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// An event describing a change to a text document. If range and rangeLength are omitted
// the new text is considered to be the full content of the document.
type TextDocumentContentChangeEvent struct {
	// The range of the document that changed.
	Range Range `json:"range"`

	// The length of the range that got replaced.
	RangeLength int `json:"rangeLength"`

	// The new text of the range/document.
	Text string `json:"text"`
}

// Descibe options to be used when registered for text document change events.
type TextDocumentChangeRegistrationOptions struct {
	TextDocumentRegistrationOptions
	// How documents are synced to the server. See TextDocumentSyncKind.Full
	// and TextDocumentSyncKindIncremental.
	SyncKind int `json:"syncKind"`
}

// The parameters send in a will save text document notification.
type WillSaveTextDocumentParams struct {
	// The document that will be saved.
	TextDocument TextDocumentIdentifier `json:"textDocument"`

	// The 'TextDocumentSaveReason'.
	Reason int `json:"reason"`
}

// Represents reasons why a text document is saved.
const (
	// Manually triggered, e.g. by the user pressing save, by starting debugging,
	// or by an API call.
	ManualSave = 1

	// Automatic after a delay.
	AfterDelaySave = 2

	// When the editor lost focus.
	FocusOutSave = 3
)

type DidSaveTextDocumentParams struct {
	// The document that was saved.
	TextDocument TextDocumentIdentifier `json:"textDocument"`

	// Optional the content when saved. Depends on the includeText value
	// when the save notifcation was requested.
	Text string `json:"text"`
}

type TextDocumentSaveRegistrationOptions struct {
	TextDocumentRegistrationOptions
	// The client is supposed to include the content on save.
	IncludeText bool `json:"includeText"`
}

type DidCloseTextDocumentParams struct {
	// The document that was closed.
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DidChangeWatchedFilesParams struct {
	// The actual file events.
	Changes []FileEvent `json:"changes"`
}

// An event describing a file change.
type FileEvent struct {
	// The file's URI.
	URI *URI `json:"uri"`

	// The change type.
	Type int `json:"int"`
}

// The file event type.
const (
	// The file got created.
	FileCreated = 1

	// The file got changed.
	FileChanged = 2

	// The file got deleted.
	FileDeleted = 3
)

type PublishDiagnosticsParams struct {
	// The URI for which diagnostic information is reported.
	URI *URI `json:"uri"`

	// An array of diagnostic information items.
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Represents a collection of [completion items](#CompletionItem) to be presented
// in the editor.
type CompletionList struct {
	// This list it not complete. Further typing should result in recomputing
	// this list.
	IsIncomplete bool `json:"isIncomplete"`

	// The completion items.
	Items []CompletionItem `json:"items"`
}

// Defines whether the insert text in a completion item should be interpreted as
// plain text or a snippet.
const (
	// The primary text to be inserted is treated as a plain string.
	PlainText = 1

	// The primary text to be inserted is treated as a snippet.
	//
	// A snippet can define tab stops and placeholders with `$1`, `$2`
	// and `${3:foo}`. `$0` defines the final tab stop, it defaults to
	// the end of the snippet. Placeholders with equal identifiers are linked,
	// that is typing in one will update others too.
	//
	// See also: https://github.com/Microsoft/vscode/blob/master/src/vs/editor/contrib/snippet/common/snippet.md
	Snippet = 2
)

type InsertTextFormat int

type CompletionItem struct {
	// The label of this completion item. By default
	// also the text that is inserted when selecting
	// this completion.
	Label string `json:"label"`

	// The kind of this completion item. Based of the kind
	// an icon is chosen by the editor.
	Kind int `json:"kind"`

	// A human-readable string with additional information
	// about this item, like type or symbol information.
	Detail string `json:"detail"`

	// A human-readable string that represents a doc-comment.
	Documentation string `json:"documentation"`

	// A string that shoud be used when comparing this item
	// with other items. When `falsy` the label is used.
	SortText string `json:"sortText"`

	// A string that should be used when filtering a set of
	// completion items. When `falsy` the label is used.
	FilterText string `json:"filterText"`

	// A string that should be inserted a document when selecting
	// this completion. When `falsy` the label is used.
	InsertText string `json:"insertText"`

	// The format of the insert text. The format applies to both the `insertText` property
	// and the `newText` property of a provided `textEdit`.
	InsertTextFormat InsertTextFormat `json:"insertTextFormat"`

	// An edit which is applied to a document when selecting this completion. When an edit is provided the value of
	// `insertText` is ignored.
	//
	// Note: The range of the edit must be a single line range and it must contain the position at which completion
	// has been requested.
	TextEdit TextEdit `json:"textEdit"`

	// An optional array of additional text edits that are applied when
	// selecting this completion. Edits must not overlap with the main edit
	// nor with themselves.
	AdditionalTextEdits []TextEdit `json:"additionalTextEdits"`

	// An optional command that is executed *after// inserting this completion. *Note// that
	// additional modifications to the current document should be described with the
	// additionalTextEdits-property.
	Command Command `json:"command"`

	// An data entry field that is preserved on a completion item between
	// a completion and a completion resolve request.
	Data map[string]interface{} `json:"data"`
}

// The kind of a completion entry.

const (
	TextCompletion        = 1
	MethodCompletion      = 2
	FunctionCompletion    = 3
	ConstructorCompletion = 4
	FieldCompletion       = 5
	VariableCompletion    = 6
	ClassCompletion       = 7
	InterfaceCompletion   = 8
	ModuleCompletion      = 9
	PropertyCompletion    = 10
	UnitCompletion        = 11
	ValueCompletion       = 12
	EnumCompletion        = 13
	KeywordCompletion     = 14
	SnippetCompletion     = 15
	ColorCompletion       = 16
	FileCompletion        = 17
	ReferenceCompletion   = 18
)

type CompletionRegistrationOptions struct {
	TextDocumentRegistrationOptions
	// The characters that trigger completion automatically.
	TriggerCharacters []string `json:"triggerCharacters"`

	// The server provides support to resolve additional
	// information for a completion item.
	ResolveProvider bool `json:"resolveProvider"`
}

// The result of a hover request.
type Hover struct {
	// The hover's content
	Contents []MarkedString `json:"contents"`

	// An optional range is a range inside a text document
	// that is used to visualize a hover, e.g. by changing the background color.
	Range Range `json:"range"`
}

// MarkedString can be used to render human readable text. It is either a markdown string
// or a code-block that provides a language and a code snippet. The language identifier
// is sematically equal to the optional language identifier in fenced code blocks in GitHub
// issues. See https://help.github.com/articles/creating-and-highlighting-code-blocks/#syntax-highlighting
//
// The pair of a language and a value is an equivalent to markdown:
// ```${language}
// ${value}
// ```
//
// Note that markdown strings will be sanitized - that means html will be escaped.
type MarkedString struct {
	Language string
	Value    string
}

func (s MarkedString) MarshalJSON() ([]byte, error) {
	if s.Language == "raw" {
		return json.Marshal(s.Value)
	}
	type markedString struct {
		Language string `json:"language"`
		Value    string `json:"value"`
	}
	return json.Marshal(markedString(s))
}

// Signature help represents the signature of something
// callable. There can be multiple signature but only one
// active and only one active parameter.
type SignatureHelp struct {
	// One or more signatures.
	Signatures []SignatureInformation `json:"signatures"`

	// The active signature. If omitted or the value lies outside the
	// range of `signatures` the value defaults to zero or is ignored if
	// `signatures.length === 0`. Whenever possible implementors should
	// make an active decision about the active signature and shouldn't
	// rely on a default value.
	// In future version of the protocol this property might become
	// mandantory to better express this.
	ActiveSignature int `json:"activeSignature"`

	// The active parameter of the active signature. If omitted or the value
	// lies outside the range of `signatures[activeSignature].parameters`
	// defaults to 0 if the active signature has parameters. If
	// the active signature has no parameters it is ignored.
	// In future version of the protocol this property might become
	// mandantory to better express the active parameter if the
	// active signature does have any.
	ActiveParameter int `json:"activeParameter"`
}

// Represents the signature of something callable. A signature
// can have a label, like a function-name, a doc-comment, and
// a set of parameters.
type SignatureInformation struct {
	// The label of this signature. Will be shown in
	// the UI.
	Label string `json:"label"`

	// The human-readable doc-comment of this signature. Will be shown
	// in the UI but can be omitted.
	Documentation string `json:"documentation"`

	// The parameters of this signature.
	Parameters []ParameterInformation `json:"parameters"`
}

// Represents a parameter of a callable-signature. A parameter can
// have a label and a doc-comment.
type ParameterInformation struct {
	// The label of this parameter. Will be shown in
	// the UI.
	Label string `json:"label"`

	// The human-readable doc-comment of this parameter. Will be shown
	// in the UI but can be omitted.
	Documentation string `json:"documentation"`
}

type SignatureHelpRegistrationOptions struct {
	TextDocumentRegistrationOptions
	// The characters that trigger signature help
	// automatically.
	TriggerCharacters []string `json:"triggerCharacters"`
}

type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
}

type ReferenceContext struct {
	// Include the declaration of the current symbol.
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// A document highlight is a range inside a text document which deserves
// special attention. Usually a document highlight is visualized by changing
// the background color of its range.
//
type DocumentHighlight struct {
	// The range this highlight applies to.
	Range Range `json:"range"`

	// The highlight kind, default is DocumentHighlightKind.Text.
	Kind int `json:"kind"`
}

// A document highlight kind.
const (
	// A textual occurrence.
	TextHighlight = 1

	// Read-access of a symbol, like reading a variable.
	ReadHighlight = 2

	// Write-access of a symbol, like writing to a variable.
	WriteHighlight = 3
)

type DocumentSymbolParams struct {
	// The text document.
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// Represents information about programming constructs like variables, classes,
// interfaces etc.
type SymbolInformation struct {
	// The name of this symbol.
	Name string `json:"name"`

	// The kind of this symbol.
	Kind int `json:"kind"`

	// The location of this symbol.
	Location Location `json:"location"`

	// The name of the symbol containing this symbol.
	ContainerName string `json:"containerName,omitempty"`
}

// A symbol kind.
const (
	SymbolFile        = 1
	SymbolModule      = 2
	SymbolNamespace   = 3
	SymbolPackage     = 4
	SymbolClass       = 5
	SymbolMethod      = 6
	SymbolProperty    = 7
	SymbolField       = 8
	SymbolConstructor = 9
	SymbolEnum        = 10
	SymbolInterface   = 11
	SymbolFunction    = 12
	SymbolVariable    = 13
	SymbolConstant    = 14
	SymbolString      = 15
	SymbolNumber      = 16
	Symbolbool        = 17
	SymbolArray       = 18
)

// The parameters of a Workspace Symbol Request.
type WorkspaceSymbolParams struct {
	// A non-empty query string
	Query string `json:"query"`
}

// Params for the CodeActionRequest
type CodeActionParams struct {
	// The document in which the command was invoked.
	TextDocument TextDocumentIdentifier `json:"textDocument"`

	// The range for which the command was invoked.
	Range Range `json:"range"`

	// Context carrying additional information.
	Context CodeActionContext `json:"context"`
}

// Contains additional diagnostic information about the context in which
// a code action is run.
type CodeActionContext struct {
	// An array of diagnostics.
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type CodeLensParams struct {
	// The document to request code lens for.
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// A code lens represents a command that should be shown along with
// source text, like the number of references, a way to run tests, etc.
//
// A code lens is _unresolved_ when no command is associated to it. For performance
// reasons the creation of a code lens and resolving should be done in two stages.
type CodeLens struct {
	// The range in which this code lens is valid. Should only span a single line.
	Range Range `json:"range"`

	// The command this code lens represents.
	Command Command `json:"command"`

	// A data entry field that is preserved on a code lens item between
	// a code lens and a code lens resolve request.
	Data map[string]interface{} `json:"data"`
}

type CodeLensRegistrationOptions struct {
	TextDocumentRegistrationOptions
	// Code lens has a resolve provider as well.
	ResolveProvider bool `json:"resolveProvider"`
}

type DocumentLinkParams struct {
	// The document to provide document links for.
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// A document link is a range in a text document that links to an internal or external resource, like another
// text document or a web site.
type DocumentLink struct {
	// The range this link applies to.
	Range Range `json:"range"`

	// The uri this link points to. If missing a resolve request is sent later.
	Target *URI `json:"target"`
}

type DocumentLinkRegistrationOptions struct {
	TextDocumentRegistrationOptions
	// Document links have a resolve provider as well.
	resolveProvider bool `json:"resolveProvider"`
}

type DocumentFormattingParams struct {
	// The document to format.
	TextDocument TextDocumentIdentifier `json:"textDocument"`

	// The format options.
	Options FormattingOptions `json:"options"`
}

// Value-object describing what options formatting should use.
type FormattingOptions struct {
	// Size of a tab in spaces.
	TabSize int `json:"tabSize"`

	// Prefer spaces over tabs.
	InsertSpaces bool `json:"insertSpaces"`
}

type DocumentRangeFormattingParams struct {
	// The document to format.
	TextDocument TextDocumentIdentifier `json:"textDocument"`

	// The range to format
	Range Range `json:"range"`

	// The format options
	Options FormattingOptions `json:"options"`
}

type DocumentOnTypeFormattingParams struct {
	// The document to format.
	TextDocument TextDocumentIdentifier `json:"textDocument"`

	// The position at which this request was sent.
	Position Position `json:"position"`

	// The character that has been typed.
	Ch string `json:"ch"`

	// The format options.
	Options FormattingOptions `json:"options"`
}

type DocumentOnTypeFormattingRegistrationOptions struct {
	TextDocumentRegistrationOptions
	// A character on which formatting should be triggered, like `}`.
	FirstTriggerCharacter string `json:"firstTriggerCharacter"`

	// More trigger characters.
	MoreTriggerCharacter []string `json:"moreTriggerCharacter"`
}

type RenameParams struct {
	// The document to format.
	TextDocument TextDocumentIdentifier `json:"textDocument"`

	// The position at which this request was sent.
	Position Position `json:"position"`

	// The new name of the symbol. If the given name is not valid the
	// request must return a [ResponseError](#ResponseError) with an
	// appropriate message set.
	NewName string `json:"newName"`
}

type ExecuteCommandParams struct {
	// The identifier of the actual command handler.
	Command string `json:"command"`

	// Arguments that the command should be invoked with.
	Arguments []map[string]interface{} `json:"arguments"`
}

// Execute command registration options.
type ExecuteCommandRegistrationOptions struct {
	// The commands to be executed on the server
	Commands []string `json:"commands"`
}

type ApplyWorkspaceEditParams struct {
	// The edits to apply.
	Edit WorkspaceEdit `json:"edit"`
}

type ApplyWorkspaceEditResponse struct {
	// Indicates whether the edit was applied or not.
	Applied bool `json:"applied"`
}
