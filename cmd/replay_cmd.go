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
	"github.com/fatih/color"
	"github.com/sidkshatriya/dontbug/engine"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"log"
	"os"
)

const (
	dontbugDefaultReplayPort            int = 9000
	dontbugDefaultGdbExtendedRemotePort int = 9999
)

var (
	gGdbExecutableFlag string
)

// replayCmd represents the replay command
var replayCmd = &cobra.Command{
	Use: `replay [flags]
  dontbug replay snaps [flags]
  `,
	Long: `
Dontbug version 0.1
Dontbug is a reversible debugger for PHP
Copyright (c) Sidharth Kshatriya 2016

dontbug replay
~~~~~~~~~~~~~~

The 'dontbug replay' command replays a previously saved execution trace to a PHP IDE debugger. You
may set breakpoints, step through code, inspect variable values etc. as you are used to. But more interestingly,
dontbug allows you to *reverse* debug i.e. step over backwards, run backwards, hit breakpoints when running in
reverse and so forth.

dontbug communicates with PHP IDEs by using the dbgp protocol which is the defacto standard for PHP IDEs
so *no special support* is required for dontbug to work with them. As far as the IDEs are concerned
they are talking with a normal PHP debug engine.

Basic Usage
-----------
- Record an execution by using 'dontbug record' (see 'dontbug record --help' to know how to do this)
- Ask your PHP ide to listen for a debugging connection
- In your favorite shell, execute:

    $ dontbug replay

- Dontbug now tries to connect to the PHP IDE that is listening for debugger connections
- Once connected, dontbug will replay the last execution recorded (via 'dontbug record') to the IDE
- Once connected, use the debugger in the IDE as you would, normally
- If you want run in reverse mode, press "r" for reverse mode and "f" for forward mode in the dontbug
  prompt. In reverse mode the buttons in your IDE will remain the same but they will have the reverse effect
  when you press them: e.g. Step Over will now be reverse Step Over and so forth.
- Press h for help on dontbug prompt for more information

Tips, Gotchas
-------------
Some PHP IDEs will try to open a browser window when they start listening for debug connections. Let them do that.
The URL they access in the browser is likely to result in an error anyways. Ignore the error. This has absolutely
no effect on dontbug as we're replaying a previously saved execution trace.

The only important thing is to look for a message in green "dontbug: Connected to PHP IDE debugger" on the dontbug
prompt. Once you see this message, you can start debugging in your PHP IDE as you normally would. Except you now
have the ability to run in reverse when you want.

                                                *-*-*
`,
	Short: "Replay and debug a previous execution",
	Run: func(cmd *cobra.Command, args []string) {
		engine.VerboseFlag = viper.GetBool("verbose")
		engine.ShowGdbNotifications = viper.GetBool("gdb-notify")

		replayPort := viper.GetInt("replay-port")
		installLocation := viper.GetString("install-location")
		targedExtendedRemotePort := viper.GetInt("gdb-remote-port")
		rrExecutable := viper.GetString("with-rr")
		gdbExecutable := viper.GetString("with-gdb")

		color.Yellow("dontbug: Using --install-location \"%v\"", installLocation)
		extDir := installLocation + "/ext/dontbug"
		_, err := os.Stat(extDir)
		if err != nil {
			log.Fatalf("'%v' does not seem to be a valid install location of dontbug. Error: %v\n", installLocation, err)
		}

		snapshotTagnamePortion := ""
		if len(args) >= 1 {
			snapshotTagnamePortion = args[0]
		}

		rrPath := engine.CheckRRExecutable(rrExecutable)
		gdbPath := engine.CheckGdbExecutable(gdbExecutable)

		engine.DoReplay(
			extDir,
			snapshotTagnamePortion,
			rrPath,
			gdbPath,
			replayPort,
			targedExtendedRemotePort,
		)
	},
}

func init() {
	RootCmd.AddCommand(replayCmd)
	replayCmd.Flags().BoolP("gdb-notify", "g", false, "show notification messages from gdb")
	replayCmd.Flags().Int("replay-port", dontbugDefaultReplayPort, "dbgp client/ide port for replaying")
	replayCmd.Flags().Int("gdb-remote-port", dontbugDefaultGdbExtendedRemotePort, "port at which rr backend should be made available to gdb")
	replayCmd.Flags().StringVar(&gGdbExecutableFlag, "with-gdb", "", "the gdb (>= 7.11.1) executable (default is to assume gdb exists in $PATH)")
}
