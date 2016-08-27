// Copyright © 2016 Sidharth Kshatriya
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"github.com/spf13/cobra"
	"os"
	"log"
	"bufio"
	"io"
	"strings"
	"os/exec"
	"github.com/kr/pty"
	"github.com/fatih/color"
	"github.com/cyrus-and/gdb"
	"net"
	"fmt"
	"time"
	"strconv"
	"bytes"
	"errors"
	"encoding/json"
)

const (
	dontbugCstepLineNumTemp int = 92
	dontbugCstepLineNum int = 95
	dontbugCpathStartsAt int = 6
	dontbugMasterBp = "1"
)

var (
	gTraceDir string
)

var gInitXMLformat string =
	`<init xmlns="urn:debugger_protocol_v1" language="PHP" protocol_version="1.0"
		fileuri="file://%v"
		appid="%v" idekey="dontbug">
		<engine version="0.0.1"><![CDATA[dontbug]]></engine>
	</init>`

var gFeatureSetResponseFormat =
	`<response xmlns="urn:debugger_protocol_v1" command="feature_set"
		transaction_id="%v" feature="%v" success="%v">
	</response>`

var gStatusResponseFormat =
	`<response xmlns="urn:debugger_protocol_v1" command="status"
		transaction_id="%v" status="%v" reason="%v">
	</response>`

var gBreakpointSetLineResponseFormat =
	`<response xmlns="urn:debugger_protocol_v1" command="breakpoint_set"
		transaction_id="%v" id="%v">
	</response>`

var gStepIntoBreakResponseFormat =
	`<response xmlns="urn:debugger_protocol_v1" xmlns:xdebug="http://xdebug.org/dbgp/xdebug" command="step_into"
		transaction_id="%v" status="break" reason="ok">
		<xdebug:message filename="%v" lineno="%v"></xdebug:message>
	</response>`

var gStepOverBreakResponseFormat =
	`<response xmlns="urn:debugger_protocol_v1" xmlns:xdebug="http://xdebug.org/dbgp/xdebug" command="step_over"
		transaction_id="%v" status="break" reason="ok">
		<xdebug:message filename="%v" lineno="%v"></xdebug:message>
	</response>`

var gEvalResponseFormat =
	`<response xmlns="urn:debugger_protocol_v1" command="eval" transaction_id="%v">
		%v
	</response>`

type DbgpCmd struct {
	Command     string // only the command name eg. stack_get
	FullCommand string // just the options after the command name
	Options     map[string]string
	Sequence    int
}

type DebugEngineState struct {
	GdbSession      *gdb.Gdb
	EntryFilePHP    string
	LastSequenceNum int
	Status          DebugEngineStatus
	Reason          DebugEngineReason
	FeatureMap      map[string]FeatureValue
	Breakpoints     map[string]*DebugEngineBreakPoint
	SourceMap       map[string]int
}

type DebugEngineStatus string
type DebugEngineReason string
type DebugEngineBreakpointType string
type DebugEngineBreakpointState string
type DebugEngineBreakpointCondition string

const (
	// The following are all PHP breakpoint types
	// Each PHP breakpoint has an entry in the DebugEngineState.Breakpoints table
	// *and* within GDB internally, of course
	breakpointTypeLine DebugEngineBreakpointType = "line"
	breakpointTypeCall DebugEngineBreakpointType = "call"
	breakpointTypeReturn DebugEngineBreakpointType = "return"
	breakpointTypeException DebugEngineBreakpointType = "exception"
	breakpointTypeConditional DebugEngineBreakpointType = "conditional"
	breakpointTypeWatch DebugEngineBreakpointType = "watch"

	// This is a non-PHP breakpoint, i.e. a pure GDB breakpoint
	// Usually internal breakpoints are not stored in the DebugEngineState.Breakpoints table
	// They are usually created and thrown away on demand
	breakpointTypeInternal DebugEngineBreakpointType = "internal"
)

func stringToBreakpointType(t string) (DebugEngineBreakpointType, error) {
	switch t {
	case "line":
		return breakpointTypeLine, nil
	case "call":
		return breakpointTypeCall, nil
	case "return":
		return breakpointTypeReturn, nil
	case "exception":
		return breakpointTypeException, nil
	case "conditional":
		return breakpointTypeConditional, nil
	case "watch":
		return breakpointTypeWatch, nil
	// Deliberately omit the internal breakpoint type
	default:
		return "", errors.New("Unknown breakpoint type")
	}
}

const (
	breakpointHitCondGtEq DebugEngineBreakpointCondition = ">="
	breakpointHitCondEq DebugEngineBreakpointCondition = "=="
	breakpointHitCondMod DebugEngineBreakpointCondition = "%"
)

const (
	breakpointStateDisabled DebugEngineBreakpointState = "disabled"
	breakpointStateEnabled DebugEngineBreakpointState = "enabled"
)

type DebugEngineBreakPoint struct {
	Id           string
	Type         DebugEngineBreakpointType
	Filename     string
	Lineno       int
	State        DebugEngineBreakpointState
	Temporary    bool
	HitCount     int
	HitValue     int
	HitCondition DebugEngineBreakpointCondition
	Exception    string
	Expression   string
}

const (
	statusStarting DebugEngineStatus = "starting"
	statusStopping DebugEngineStatus = "stopping"
	statusRunning DebugEngineStatus = "running"
	statusBreak DebugEngineStatus = "break"
)

const (
	reasonOk DebugEngineReason = "ok"
	reasonError DebugEngineReason = "error"
	reasonAborted DebugEngineReason = "aborted"
	reasonExeception DebugEngineReason = "exception"
)

type FeatureBool struct{ Value bool; ReadOnly bool }
type FeatureInt struct{ Value int; ReadOnly bool }
type FeatureString struct{ Value string; ReadOnly bool }

type FeatureValue interface {
	Set(value string)
	String() string
}

func (this *FeatureBool) Set(value string) {
	if this.ReadOnly {
		log.Fatal("Trying assign to a read only value")
	}

	if value == "0" {
		this.Value = false
	} else if value == "1" {
		this.Value = true
	} else {
		log.Fatal("Trying to assign a non-boolean value to a boolean.")
	}
}

func (this FeatureBool) String() string {
	if this.Value {
		return "1"
	} else {
		return "0"
	}
}

func (this *FeatureString) Set(value string) {
	if this.ReadOnly {
		log.Fatal("Trying assign to a read only value")
	}
	this.Value = value
}

func (this FeatureInt) String() string {
	return strconv.Itoa(this.Value)
}

func (this *FeatureInt) Set(value string) {
	if this.ReadOnly {
		log.Fatal("Trying assign to a read only value")
	}
	var err error
	this.Value, err = strconv.Atoi(value)
	if err != nil {
		log.Fatal(err)
	}

}

func (this FeatureString) String() string {
	return this.Value
}

func initFeatureMap() map[string]FeatureValue {
	var featureMap = map[string]FeatureValue{
		"language_supports_threads" : &FeatureBool{false, true},
		"language_name" : &FeatureString{"PHP", true},
		"language_version" : &FeatureString{"7.0", true},
		"encoding" : &FeatureString{"ISO-8859-1", true},
		"protocol_version" : &FeatureInt{1, true},
		"supports_async" : &FeatureBool{false, true},
		"breakpoint_types" : &FeatureString{"line call return exception conditional watch", true},
		"multiple_sessions" : &FeatureBool{false, false},
		"max_children": &FeatureInt{64, false},
		"max_data": &FeatureInt{2048, false},
		"max_depth" : &FeatureInt{1, false},
		"extended_properties": &FeatureBool{false, false},
		"show_hidden": &FeatureBool{false, false},
	}

	return featureMap
}

// replayCmd represents the replay command
var replayCmd = &cobra.Command{
	Use:   "replay [optional-trace-dir]",
	Short: "Replay and debug a previous execution",
	Run: func(cmd *cobra.Command, args []string) {
		if (len(gExtDir) <= 0) {
			color.Yellow("dontbug: No --ext-dir provided, assuming \"ext/dontbug\"")
			gExtDir = "ext/dontbug"
		}

		if len(args) < 1 {
			color.Yellow("dontbug: No trace directory provided, latest-trace trace directory assumed")
			gTraceDir = ""
		} else {
			gTraceDir = args[0]
		}

		bpMap := constructBreakpointLocMap(gExtDir)
		engineState := startReplayInRR(gTraceDir, bpMap)
		debuggerIdeCmdLoop(engineState)
	},
}

func init() {
	RootCmd.AddCommand(replayCmd)
}

func debuggerIdeCmdLoop(es *DebugEngineState) {
	color.Yellow("dontbug: Trying to connect to debugger IDE")
	conn, err := net.Dial("tcp", ":9000")
	if err != nil {
		log.Fatal(err)
	}
	color.Green("dontbug: Connected to debugger IDE (aka \"client\")")

	// send the init packet
	payload := fmt.Sprintf(gInitXMLformat, es.EntryFilePHP, os.Getpid())
	packet := constructDbgpPacket(payload)
	conn.Write(packet)
	color.Green("dontbug -> ide:\n%v", payload)

	reverse := false
	go func() {
		for {
			var userResponse string
			var a [4]string // @TODO remove kludge
			fmt.Scanln(&userResponse, &a[0], &a[1], &a[2], &a[3])

			if strings.HasPrefix(userResponse, "t") {
				reverse = !reverse
				if reverse {
					color.Red("CHANGED TO: reverse debugging mode")
				} else {
					color.Red("CHANGED TO: forward debugging mode")
				}
			} else if strings.HasPrefix(userResponse, "-") {
				// @TODO remove kludge
				result := sendGdbCommand(es.GdbSession, fmt.Sprintf("%v %v %v %v %v", userResponse[1:], a[0], a[1], a[2], a[3]));

				jsonResult, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					log.Fatal(err)
				}
				fmt.Println(string(jsonResult))
			} else if strings.HasPrefix(userResponse, "q") {
				// @TODO is this sufficient to ensure a cleanshutdown?
				es.GdbSession.Exit()
				os.Exit(0)
			} else {
				if reverse {
					color.Green("CURRENTLY IN: reverse debugging mode")
				} else {
					color.Green("CURRENTLY IN: forward debugging mode")
				}
			}
			fmt.Print("dontbug prompt>")
		}
	}()

	buf := bufio.NewReader(conn)
	for {
		command, err := buf.ReadString(byte(0))
		command = strings.TrimRight(command, "\x00")
		if err != nil {
			log.Fatal(err)
		}
		color.Cyan("\nide -> dontbug: %v", command)
		payload = handleIdeRequest(es, command, reverse)
		conn.Write(constructDbgpPacket(payload))
		continued := ""
		if len(payload) > 100 {
			continued = "..."
		}
		color.Green("dontbug -> ide:\n%.300v%v", payload, continued)
		fmt.Print("dontbug prompt>")
	}
}

func constructDbgpPacket(payload string) []byte {
	header_xml := "<?xml version=\"1.0\" encoding=\"iso-8859-1\"?>\n"
	var buf bytes.Buffer
	buf.WriteString(strconv.Itoa(len(payload) + len(header_xml)))
	buf.Write([]byte{0})
	buf.WriteString(header_xml)
	buf.WriteString(payload)
	buf.Write([]byte{0})
	return buf.Bytes()
}

func handleIdeRequest(es *DebugEngineState, command string, reverse bool) string {
	dbgpCmd := parseCommand(command)
	if es.LastSequenceNum > dbgpCmd.Sequence {
		log.Fatal("Sequence number", dbgpCmd.Sequence, "has already been seen")
	}

	es.LastSequenceNum = dbgpCmd.Sequence
	switch(dbgpCmd.Command) {
	case "feature_set":
		return handleFeatureSet(es, dbgpCmd)
	case "status":
		return handleStatus(es, dbgpCmd)
	case "breakpoint_set":
		return handleBreakpointSet(es, dbgpCmd)
	case "step_into":
		return handleStepInto(es, dbgpCmd, reverse)
	case "step_over":
		return handleStepOver(es, dbgpCmd, reverse)
	case "eval":
		return handleWithNoGdbBreakpoints(es, dbgpCmd)
	case "stack_get":
		fallthrough
	case "stack_depth":
		fallthrough
	case "context_names":
		fallthrough
	case "context_get":
		return handleWithNoGdbBreakpoints(es, dbgpCmd)
	case "typemap_get":
		fallthrough
	case "property_get":
		fallthrough
	case "property_value":
		return handleStandard(es, dbgpCmd)
	case "stop":
		handleStop(es)
	default:
		es.SourceMap = nil // Just to reduce size of map dump
		fmt.Println(es)
		log.Fatal("Unimplemented command:", command)
	}

	return ""
}

func handleStop(es *DebugEngineState) {
	color.Yellow("IDE asked dontbug engine to stop. Exiting...")
	// @TODO Does this lead to a fully clean exit?
	es.GdbSession.Exit()
	os.Exit(0)
}

func handleStandard(es *DebugEngineState, dCmd DbgpCmd) string {
	result := xSlashSgdb(es, fmt.Sprintf("dontbug_xdebug_cmd(\"%v\")", dCmd.FullCommand))
	return result
}

func handleWithNoGdbBreakpoints(es *DebugEngineState, dCmd DbgpCmd) string {
	bpList := getEnabledPhpBreakpoints(es)
	disableAllGdbBreakpoints(es)
	result := xSlashSgdb(es, fmt.Sprintf("dontbug_xdebug_cmd(\"%v\")", dCmd.FullCommand))
	enableGdbBreakpoints(es, bpList)
	return result
}

func getEnabledPhpBreakpoints(es *DebugEngineState) []string {
	var enabledPhpBreakpoints []string
	for name, bp := range es.Breakpoints {
		if bp.State == breakpointStateEnabled && bp.Type != breakpointTypeInternal {
			enabledPhpBreakpoints = append(enabledPhpBreakpoints, name)
		}
	}

	return enabledPhpBreakpoints
}

func disableGdbBreakpoints(es *DebugEngineState, bpList []string) {
	commandArgs := fmt.Sprintf("%v", strings.Join(bpList, " "))
	sendGdbCommand(es.GdbSession, "break-disable", commandArgs)
	for _, el := range bpList {
		bp, ok := es.Breakpoints[el]
		if ok {
			bp.State = breakpointStateDisabled
		}
	}
}

// convenience function
func disableGdbBreakpoint(es *DebugEngineState, bp string) {
	disableGdbBreakpoints(es, []string{bp})
}

// Note that not all "internal" breakpoints are stored in the breakpoints table
func disableAllGdbBreakpoints(es *DebugEngineState) {
	sendGdbCommand(es.GdbSession, "break-disable")
	for _, bp := range es.Breakpoints {
		bp.State = breakpointStateDisabled
	}
}

func enableAllGdbBreakpoints(es *DebugEngineState) {
	sendGdbCommand(es.GdbSession, "break-enable")
	for _, bp := range es.Breakpoints {
		bp.State = breakpointStateEnabled
	}
}

func enableGdbBreakpoints(es *DebugEngineState, bpList []string) {
	commandArgs := fmt.Sprintf("%v", strings.Join(bpList, " "))
	sendGdbCommand(es.GdbSession, "break-enable", commandArgs)
	for _, el := range bpList {
		bp, ok := es.Breakpoints[el]
		if ok {
			bp.State = breakpointStateEnabled
		}
	}
}

// convenience function
func enableGdbBreakpoint(es *DebugEngineState, bp string) {
	enableGdbBreakpoints(es, []string{bp})
}

// Sets an equivalent breakpoint in gdb for PHP
// Also inserts the breakpoint into es.Breakpoints table
func setPhpBreakpointInGdbEx(es *DebugEngineState, phpFilename string, phpLineno int, disabled bool) string {
	internalLineno, ok := es.SourceMap[phpFilename]
	if !ok {
		log.Fatal("Not able to find ", phpFilename, " to add a breakpoint. You need to run 'dontbug generate' specific to this project, most likely")
	}

	breakpointState := breakpointStateEnabled
	disabledFlag := ""
	if disabled {
		disabledFlag = "-d " // Note the space after -d
		breakpointState = breakpointStateDisabled
	}

	// @TODO for some reason this break-insert command stops working if we break sendGdbCommand call into operation, argument params
	result := sendGdbCommand(es.GdbSession,
		fmt.Sprintf("break-insert %v-f -c \"lineno == %v\" --source dontbug_break.c --line %v", disabledFlag, phpLineno, internalLineno))

	if result["class"] != "done" {
		log.Fatal("Breakpoint was not set successfully")
	}

	payload := result["payload"].(map[string]interface{})
	bkpt := payload["bkpt"].(map[string]interface{})
	id := bkpt["number"].(string)

	_, ok = es.Breakpoints[id]
	if ok {
		log.Fatal("Breakpoint number not unique: ", id)
	}

	es.Breakpoints[id] = &DebugEngineBreakPoint{
		Id:id,
		Filename:phpFilename,
		Lineno:phpLineno,
		State:breakpointState,
		Temporary:false,
		Type:breakpointTypeLine,
	}

	return id
}

// Does not make an entry in breakpoints table
func setPhpStackLevelBreakpointInGdb(es *DebugEngineState, level string) string {
	// @TODO for some reason this break-insert command stops working if we break sendGdbCommand call into operation, argument params
	result := sendGdbCommand(es.GdbSession,
		fmt.Sprintf("break-insert -t -f -c \"level <= %v\" --source dontbug.c --line %v", level, dontbugCstepLineNum))

	if result["class"] != "done" {
		log.Fatal("Breakpoint was not set successfully")
	}

	payload := result["payload"].(map[string]interface{})
	bkpt := payload["bkpt"].(map[string]interface{})
	id := bkpt["number"].(string)

	return id
}

func removeGdbBreakpoint(es *DebugEngineState, id string) {
	sendGdbCommand(es.GdbSession, "break-delete", id)
	_, ok := es.Breakpoints[id]
	if ok {
		delete(es.Breakpoints, id)
	}
}

// Algorithm:
// 1. Disable all breakpoints
// 2. Enable breakpoint 1
// 3. exec-continue
// 4. GDB will break on breakpoint 1, get lineno and fileno, send XML response
// 5. Disable breakpoint 1
// 6. Restore all breakpoints disabled in point 1
func handleStepInto(es *DebugEngineState, dCmd DbgpCmd, reverse bool) string {
	disableAllGdbBreakpoints(es) // @TODO remove!

	enableGdbBreakpoint(es, dontbugMasterBp)
	continueExection(es, reverse)
	disableGdbBreakpoint(es, dontbugMasterBp)

	filename := xSlashSgdb(es, "filename")
	lineno := xSlashDgdb(es, "lineno")
	return fmt.Sprintf(gStepIntoBreakResponseFormat, dCmd.Sequence, filename, lineno)
}

func handleStepOver(es *DebugEngineState, dCmd DbgpCmd, reverse bool) string {
	disableAllGdbBreakpoints(es) // @TODO remove!

	currentPhpStackLevel := xGdbCmdValue(es, "level")

	// We're interested in maintaining or decreasing the stack level for step
	id := setPhpStackLevelBreakpointInGdb(es, currentPhpStackLevel)
	continueExection(es, reverse)

	// @TODO while doing step-next you could trigger a PHP breakpoint

	// Though this is a temporary breakpoint, it may not have been triggered.
	removeGdbBreakpoint(es, id)

	filename := xSlashSgdb(es, "filename")
	phpLineno := xSlashDgdb(es, "lineno")

	return fmt.Sprintf(gStepOverBreakResponseFormat, dCmd.Sequence, filename, phpLineno)
}

func continueExection(es *DebugEngineState, reverse bool) {
	if (reverse) {
		sendGdbCommand(es.GdbSession, "exec-continue", "--reverse")
	} else {
		sendGdbCommand(es.GdbSession, "exec-continue")
	}
}
func handleFeatureSet(es *DebugEngineState, dCmd DbgpCmd) string {
	n, ok := dCmd.Options["n"]
	if !ok {
		log.Fatal("Please provide -n option in feature_set")
	}

	v, ok := dCmd.Options["v"]
	if !ok {
		log.Fatal("Not provided v option in feature_set")
	}

	var featureVal FeatureValue
	featureVal, ok = es.FeatureMap[n]
	if !ok {
		log.Fatal("Unknown option:", n)
	}

	featureVal.Set(v)
	return fmt.Sprintf(gFeatureSetResponseFormat, dCmd.Sequence, n, 1)
}

func handleStatus(es *DebugEngineState, dCmd DbgpCmd) string {
	return fmt.Sprintf(gStatusResponseFormat, dCmd.Sequence, es.Status, es.Reason)
}

func handleBreakpointSet(es *DebugEngineState, dCmd DbgpCmd) string {
	t, ok := dCmd.Options["t"]
	if (!ok) {
		log.Fatal("Please provide breakpoint type option -t in breakpoint_set")
	}

	tt, err := stringToBreakpointType(t)
	if err != nil {
		log.Fatal(err)
	}

	switch tt {
	case breakpointTypeLine:
		return handleBreakpointSetLineBreakpoint(es, dCmd)
	default:
		log.Fatal("Unimplemented breakpoint type")
	}

	return ""
}

// @TODO deal with breakpoints on non-existent files
func handleBreakpointSetLineBreakpoint(es *DebugEngineState, dCmd DbgpCmd) string {
	phpFilename, ok := dCmd.Options["f"]
	if !ok {
		log.Fatal("Please provide filename option -f in breakpoint_set")
	}

	status, ok := dCmd.Options["s"]
	disabled := false
	if ok && status == "disabled " {
		disabled = true
	}

	phpLinenoString, ok := dCmd.Options["n"]
	if !ok {
		log.Fatal("Please provide line number option -n in breakpoint_set")
	}

	phpLineno, err := strconv.Atoi(phpLinenoString)
	if err != nil {
		log.Fatal(err)
	}

	id := setPhpBreakpointInGdbEx(es, phpFilename, phpLineno, disabled)
	return fmt.Sprintf(gBreakpointSetLineResponseFormat, dCmd.Sequence, id)
}

func xSlashSgdb(es *DebugEngineState, expression string) string {
	resultString := xGdbCmdValue(es, expression)
	finalString, err := parseGdbStringResponse(resultString)
	if err != nil {
		log.Fatal(finalString)
	}
	return finalString

}

func xSlashDgdb(es *DebugEngineState, expression string) int {
	resultString := xGdbCmdValue(es, expression)
	intResult, err := strconv.Atoi(resultString)
	if err != nil {
		log.Fatal(err)
	}
	return intResult
}

func xGdbCmdValue(es *DebugEngineState, expression string) string {
	result := sendGdbCommand(es.GdbSession, "data-evaluate-expression", expression)
	class, ok := result["class"]

	commandWas := "data-evaluate-expression " + expression
	if !ok {
		log.Fatal("Could not execute the gdb/mi command: ", commandWas)
	}

	if class != "done" {
		log.Fatal("Could not execute the gdb/mi command: ", commandWas)
	}

	payload := result["payload"].(map[string]interface{})
	resultString := payload["value"].(string)

	return resultString
}

func parseCommand(fullCommand string) DbgpCmd {
	components := strings.Fields(fullCommand)
	flags := make(map[string]string)
	command := components[0]
	for i, v := range components[1:] {
		if (i % 2 == 1) {
			continue
		}

		// Also remove the leading "-" in the flag via [1:]
		flags[strings.TrimSpace(v)[1:]] = strings.TrimSpace(components[i + 2])
	}

	seq, ok := flags["i"]
	if !ok {
		log.Fatal("Could not find sequence number in command")
	}

	seqInt, err := strconv.Atoi(seq)
	if err != nil {
		log.Fatal(err)
	}

	return DbgpCmd{command, fullCommand, flags, seqInt}
}

func constructBreakpointLocMap(extensionDir string) map[string]int {
	absExtDir := getDirAbsPath(extensionDir)
	dontbugBreakFilename := absExtDir + "/dontbug_break.c"
	fmt.Println("dontbug: Looking for dontbug_break.c in", absExtDir)

	file, err := os.Open(dontbugBreakFilename)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	fmt.Println("dontbug: Found", dontbugBreakFilename)
	bpLocMap := make(map[string]int, 1000)
	buf := bufio.NewReader(file)
	lineno := 0
	for {
		line, err := buf.ReadString('\n')
		lineno++
		if err == io.EOF {
			break
		} else if (err != nil) {
			log.Fatal(err)
		}

		index := strings.Index(line, "//###")
		if index == -1 {
			continue
		}

		filename := strings.TrimSpace("file://" + line[index + dontbugCpathStartsAt:])
		bpLocMap[filename] = lineno
	}

	fmt.Println("dontbug: Completed building association of filename and linenumbers for breakpoints")
	return bpLocMap
}

func startReplayInRR(traceDir string, bpMap map[string]int) *DebugEngineState {
	absTraceDir := ""
	if len(traceDir) > 0 {
		absTraceDir = getDirAbsPath(traceDir)
	}

	// Start an rr replay session
	replaySession := exec.Command("rr", "replay", "-s", "9999", absTraceDir)
	fmt.Println("dontbug: Using rr at:", replaySession.Path)
	f, err := pty.Start(replaySession)
	if err != nil {
		log.Fatal(err)
	}
	color.Green("dontbug: Successfully started replay session")

	// Abort if we are not able to get the gdb connection string within 10 sec
	cancel := make(chan bool, 1)
	go func() {
		time.Sleep(10 * time.Second)
		select {
		case <-cancel:
			return
		default:
			log.Fatal("Could not find gdb connection string that is given by rr")
		}
	}()

	// Get hardlink filename which will be needed for gdb debugging
	buf := bufio.NewReader(f)
	for {
		line, _ := buf.ReadString('\n')
		fmt.Println(line)
		if strings.Contains(line, "target extended-remote") {
			cancel <- true
			close(cancel)

			go io.Copy(os.Stdout, f)
			slashAt := strings.Index(line, "/")

			hardlinkFile := strings.TrimSpace(line[slashAt:])
			return startGdbAndInitDebugEngineState(hardlinkFile, bpMap)
		}
	}

	// @TODO is this correct??
	go func() {
		err := replaySession.Wait()
		if err != nil {
			log.Fatal(err)
		}
	}()

	return nil
}

// Starts gdb and creates a new DebugEngineState object
func startGdbAndInitDebugEngineState(hardlinkFile string, bpMap map[string]int) *DebugEngineState {
	gdbArgs := []string{"gdb", "-l", "-1", "-ex", "target extended-remote :9999", "--interpreter", "mi", hardlinkFile}
	fmt.Println("dontbug: Starting gdb with the following string:", strings.Join(gdbArgs, " "))

	/*gdbSession, err := gdb.NewCmd(gdbArgs, func(notification map[string]interface{}) {
		fmt.Println("%v", notification)
	})*/
	gdbSession, err := gdb.NewCmd(gdbArgs, nil)
	if err != nil {
		log.Fatal(err)
	}

	go io.Copy(os.Stdout, gdbSession)

	// This is our usual steppping breakpoint. Initially disabled.
	miArgs := fmt.Sprintf("-f -d --source dontbug.c --line %v", dontbugCstepLineNum)
	result := sendGdbCommand(gdbSession, "break-insert", miArgs)

	// Note that this is a temporary breakpoint, just to get things started
	miArgs = fmt.Sprintf("-t -f --source dontbug.c --line %v", dontbugCstepLineNumTemp)
	sendGdbCommand(gdbSession, "break-insert", miArgs)

	// Unlimited print length in gdb so that results from gdb are not "chopped" off
	sendGdbCommand(gdbSession, "gdb-set", "print elements 0")

	// Should break on line: dontbugCstepLineNum - 1 of dontbug.c
	sendGdbCommand(gdbSession, "exec-continue")

	result = sendGdbCommand(gdbSession, "data-evaluate-expression", "filename")
	payload := result["payload"].(map[string]interface{})
	filename := payload["value"].(string)
	properFilename, err := parseGdbStringResponse(filename)

	if err != nil {
		log.Fatal(properFilename)
	}

	es := &DebugEngineState{
		GdbSession: gdbSession,
		FeatureMap:initFeatureMap(),
		EntryFilePHP:properFilename,
		Status:statusStarting,
		Reason:reasonOk,
		SourceMap:bpMap,
		LastSequenceNum:0,
		Breakpoints:make(map[string]*DebugEngineBreakPoint, 10),
	}

	// "1" is always the first breakpoint number in gdb
	// Its used for stepping
	es.Breakpoints["1"] = &DebugEngineBreakPoint{
		Id:"1",
		Lineno:dontbugCstepLineNum,
		Filename:"dontbug.c",
		State:breakpointStateDisabled,
		Temporary:false,
		Type:breakpointTypeInternal,
	}

	return es
}

// a gdb string response looks like '0x7f261d8624e8 "some string here"'
// empty string looks '0x7f44a33a9c1e ""'
func parseGdbStringResponse(gdbResponse string) (string, error) {
	first := strings.Index(gdbResponse, "\"")
	last := strings.LastIndex(gdbResponse, "\"")

	if (first == last || first == -1 || last == -1) {
		return "", errors.New("Improper gdb data-evaluate-expression string response")
	}

	unquote := unquoteGdbStringResult(gdbResponse[first + 1:last])
	return unquote, nil
}

func unquoteGdbStringResult(input string) string {
	l := len(input)
	var buf bytes.Buffer
	skip := false
	for i, c := range input {
		if skip {
			skip = false
			continue
		}

		if c == '\\' && i < l && input[i + 1] == '"' {
			buf.WriteRune('"')
			skip = true
		} else {
			buf.WriteRune(c)
		}
	}

	return buf.String()
}

func sendGdbCommand(gdbSession *gdb.Gdb, command string, arguments ...string) map[string]interface{} {
	color.Green("dontbug -> gdb: %v %v", command, strings.Join(arguments, " "))
	result, err := gdbSession.Send(command, arguments...)
	if err != nil {
		log.Fatal(err)
	}

	continued := ""
	if (len(result) > 100) {
		continued = "..."
	}
	color.Cyan("gdb -> dontbug: %.300v%v", result, continued)
	return result
}
