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

package engine

import (
	"bytes"
	"fmt"
	"github.com/fatih/color"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"
)

var gBreakCskeletonHeader = `
/*
 * Copyright 2016 Sidharth Kshatriya
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/**
 * This file was autogenerated by dontbug on ` + time.Now().String() + `
 * IMPORTANT -- DO NOT remove/edit/move comments with ### or $$$ or &&&
 */
#include "php.h"
#include "php_dontbug.h"

void dontbug_break_location(zend_string* zfilename, zend_execute_data *execute_data, int lineno, unsigned long level) {
    zend_ulong hash = zfilename->h;
    char *filename = ZSTR_VAL(zfilename);
`

var gBreakCskeletonFooter = `
}
`

var gLevelLocationHeader = `
void dontbug_level_location(unsigned long level, char* filename, int lineno) {
    int count = 0;
`

var gLevelLocationFooter = `
}
`

type myUintArray []uint64
type myMap map[uint64][]string

func (arr myUintArray) Len() int {
	return len(arr)
}

func (arr myUintArray) Less(i, j int) bool {
	return arr[i] < arr[j]
}

func (arr myUintArray) Swap(i, j int) {
	arr[j], arr[i] = arr[i], arr[j]
}

func makeDontbugExtension(extDir string, phpPath string) {
	extDirAbsPath := getAbsNoSymlinkPath(extDir)

	// Save the working directory
	cwd, err := os.Getwd()
	fatalIf(err)

	phpizePath := path.Clean(path.Dir(phpPath) + "/phpize")
	phpConfigPath := path.Clean(path.Dir(phpPath) + "/php-config")

	Verbosef("Trying to find phpize (%v) corresponding to the php executable (%v)\n", phpizePath, phpPath)
	_, err = os.Stat(phpizePath)
	if err != nil {
		log.Fatal("Not able to find 'phpize'. Error: ", err)
	}

	Verbosef("Trying to find php-config (%v) corresponding to the php executable (%v)\n", phpConfigPath, phpPath)
	_, err = os.Stat(phpConfigPath)
	if err != nil {
		log.Fatal("Not able to find 'php-config'. Error: ", err)
	}

	os.Chdir(extDirAbsPath)
	_, err = os.Stat(path.Clean(extDirAbsPath + "/Makefile"))
	// If the file exists
	if err == nil {
		makeDistClean, err := exec.Command("make", "distclean").CombinedOutput()
		if err != nil {
			fmt.Println(string(makeDistClean))
			log.Fatal(err)
		} else {
			Verboseln(string(makeDistClean))
			color.Green("dontbug: Successfully ran 'make distclean' in dontbug zend extension directory")
		}
	}

	phpizeOut, err := exec.Command(phpizePath).CombinedOutput()
	if err != nil {
		fmt.Println(string(phpizeOut))
		log.Fatal(err)
	} else {
		Verboseln(string(phpizeOut))
		color.Green("dontbug: Successfully ran phpize in dontbug zend extension directory")
	}

	color.Green("dontbug: Running configure in dontbug zend extension directory")
	configureScript := path.Clean(extDirAbsPath + "/configure")
	configureOut, err := exec.Command(configureScript, fmt.Sprintf("--with-php-config=%v", phpConfigPath)).CombinedOutput()
	if err != nil {
		fmt.Println(string(configureOut))
		log.Fatal(err)
	} else {
		Verboseln(string(configureOut))
		color.Green("dontbug: Successfully ran configure in dontbug zend extension directory")
	}

	makeOutput, err := exec.Command("make", "CFLAGS=-g -O0").CombinedOutput()
	if err != nil {
		fmt.Println(string(makeOutput))
		log.Fatal(err)
	} else {
		Verboseln(string(makeOutput))
		color.Green("dontbug: Successfully compiled the dontbug zend extension")
	}

	// Restore the old working directory
	os.Chdir(cwd)
}

func doGeneration(rootAbsNoSymPathDir, extDirAbsNoSymPath string, maxStackDepth int, phpPath string) {
	generateBreakFile(rootAbsNoSymPathDir, extDirAbsNoSymPath, gBreakCskeletonHeader, gBreakCskeletonFooter, gLevelLocationHeader, gLevelLocationFooter, maxStackDepth)
	makeDontbugExtension(extDirAbsNoSymPath, phpPath)
}

func generateBreakFile(rootDirAbsNoSymPath, extDirAbsNoSymPath, skelHeader, skelFooter, skelLocHeader, skelLocFooter string, maxStackDepth int) {
	// Open the dontbug_break.c file for generation
	breakFileName := path.Clean(extDirAbsNoSymPath + "/dontbug_break.c")
	f, err := os.Create(breakFileName)
	fatalIf(err)
	defer f.Close()

	color.Green("dontbug: Generating %v for all PHP code in: %v", breakFileName, rootDirAbsNoSymPath)
	// All is good, now go ahead and do some real work
	ar, m := makeMap(rootDirAbsNoSymPath)
	fmt.Fprintf(f, "%v%v\n", numFilesSentinel, len(ar))
	fmt.Fprintf(f, "%v%v\n", maxStackDepthSentinel, maxStackDepth)
	fmt.Fprintln(f, skelHeader)
	fmt.Fprintln(f, generateFileBreakBody(ar, m))
	fmt.Fprintln(f, skelFooter)
	fmt.Fprintln(f, skelLocHeader)
	fmt.Fprintln(f, generateLocBody(maxStackDepth))
	fmt.Fprintln(f, skelLocFooter)

	color.Green("dontbug: Code generation complete. Compiling dontbug zend extension...")
}

func generateLocBody(maxStackDepth int) string {
	var buf bytes.Buffer

	for level := 0; level < maxStackDepth; level++ {
		buf.WriteString(fmt.Sprintf("    if (level <= %v) {\n", level))
		buf.WriteString(fmt.Sprintf("        count++; %v %v\n", levelSentinel, level))
		buf.WriteString(fmt.Sprint("    }\n"))
	}

	return buf.String()
}

func allFilesHelper(directory string, phpFilesMap map[string]int, visited map[string]bool) {
	filepath.Walk(directory, func(pathEntry string, info os.FileInfo, err error) error {
		fatalIf(err)

		if info.IsDir() {
			visited[pathEntry] = true
		}

		if info.Mode()&os.ModeSymlink != 0 {
			pathEntry, err = filepath.EvalSymlinks(pathEntry)
			fatalIf(err)

			info, err = os.Stat(pathEntry)
			fatalIf(err)

			if info.IsDir() && !visited[pathEntry] {
				allFilesHelper(pathEntry, phpFilesMap, visited)
				return nil
			}
		}

		// @TODO make this more generic. Get extensions from a yaml file??
		if (info.Mode()&os.ModeType == 0) &&
			(path.Ext(pathEntry) == ".php" ||
				path.Ext(pathEntry) == ".module" ||
				path.Ext(pathEntry) == ".install") {
			phpFilesMap[pathEntry] = 1
		}

		return nil
	})
}

func allFiles(directory string) map[string]int {
	phpFilesMap := make(map[string]int)
	visited := make(map[string]bool)
	allFilesHelper(directory, phpFilesMap, visited)
	return phpFilesMap
}

// Repeat a space n times
func s(n int) string {
	return strings.Repeat(" ", n)
}

func ifThenElse(ifc, ifb, elseifc, elseifb, elseb string, indent int) string {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("%vif (%v) {\n", s(indent), ifc))
	buf.WriteString(fmt.Sprintf("%v", ifb))
	buf.WriteString(fmt.Sprintf("%v} else if (%v) {\n", s(indent), elseifc))
	buf.WriteString(fmt.Sprintf("%v", elseifb))
	buf.WriteString(fmt.Sprintf("%v} else {\n", s(indent)))
	buf.WriteString(fmt.Sprintf("%v", elseb))
	buf.WriteString(fmt.Sprintf("%v}\n", s(indent)))
	return buf.String()
}

func ifThen(ifc, ifb, elseb string, indent int) string {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("%vif (%v) {\n", s(indent), ifc))
	buf.WriteString(fmt.Sprintf("%v", ifb))
	buf.WriteString(fmt.Sprintf("%v} else {\n", s(indent)))
	buf.WriteString(fmt.Sprintf("%v", elseb))
	buf.WriteString(fmt.Sprintf("%v}\n", s(indent)))
	return buf.String()
}

func eq(rhs uint64) string {
	return fmt.Sprintf("hash == Z_UL(%v)", rhs)
}

func lt(rhs uint64) string {
	return fmt.Sprintf("hash < Z_UL(%v)", rhs)
}

// @TODO deal with hash collisions
func foundHash(hash uint64, matchingFiles []string, indent int) string {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("%v// hash == %v\n", s(indent), hash))
	buf.WriteString(fmt.Sprintf("%vreturn; %v %v\n", s(indent), phpFilenameSentinel, matchingFiles[0]))
	return buf.String()
}

// "Daniel J. Bernstein, Times 33 with Addition" string hashing algorithm
// Its the string hashing algorithm used by PHP.
// See https://github.com/php/php-src/blob/PHP-7.0.9/Zend/zend_string.h#L291 for the C language implementation
//
// (64 bit version of function. For 32 bit version see below)
//
func djbx33a64(byteStr string) uint64 {
	var hash uint64 = 5381
	i := 0

	length := len(byteStr)
	for ; length >= 8; length = length - 8 {
		for j := 0; j < 8; j++ {
			hash = ((hash << 5) + hash) + uint64(byteStr[i])
			i++
		}
	}

	for j := length; j >= 1; j-- {
		hash = ((hash << 5) + hash) + uint64(byteStr[i])
		i++
	}

	return hash | (1 << 63)
}

// This is the 32 bit version of djbx33a
// See djbx33a64() above for more information about this function
func djbx33a32(byteStr string) uint32 {
	var hash uint32 = 5381
	i := 0

	length := len(byteStr)
	for ; length >= 8; length = length - 8 {
		for j := 0; j < 8; j++ {
			hash = ((hash << 5) + hash) + uint32(byteStr[i])
			i++
		}
	}

	for j := length; j >= 1; j-- {
		hash = ((hash << 5) + hash) + uint32(byteStr[i])
		i++
	}

	return hash | (1 << 31)
}

func makeMap(rootAbsNoLinkPath string) (myUintArray, myMap) {
	longIs64bits := false
	if unsafe.Sizeof(uint(0)) == 8 {
		longIs64bits = true
	}

	filesMap := allFiles(rootAbsNoLinkPath)
	color.Green("dontbug: %v PHP files found", len(filesMap))

	m := make(myMap)
	hashAr := make(myUintArray, 0, 100)
	var hash uint64
	for fileName := range filesMap {
		if longIs64bits {
			hash = djbx33a64(fileName)
		} else {
			// This is OK cause we're just interested in how the numeric literals print out during code generation
			hash = uint64(djbx33a32(fileName))
		}

		_, ok := m[hash]
		if ok {
			// @TODO
			log.Fatal("Hash collision! Currently unimplemented\n")
		} else {
			m[hash] = []string{fileName}
			hashAr = append(hashAr, hash)
		}
	}
	sort.Sort(hashAr)

	if len(hashAr) == 0 || len(m) == 0 {
		log.Fatal("Error in makeMap. No entries")
	}
	return hashAr, m
}

func generateFileBreakBody(arr myUintArray, m myMap) string {
	length := len(arr)
	return generateBreakHelper(arr, m, 0, length-1, 4)
}

func generateBreakHelper(arr myUintArray, m myMap, low, high, indent int) string {
	if high == low {
		return foundHash(arr[low], m[arr[low]], indent)
	}

	mid := (high + low) / 2
	// Can only happen when we have two elements left
	if mid == low {
		return ifThen(eq(arr[mid]),
			foundHash(arr[mid], m[arr[mid]], indent+4),
			foundHash(arr[high], m[arr[high]], indent+4),
			indent)
	}

	return ifThenElse(eq(arr[mid]),
		foundHash(arr[mid], m[arr[mid]], indent+4),
		lt(arr[mid]),
		generateBreakHelper(arr, m, low, mid-1, indent+4),
		generateBreakHelper(arr, m, mid+1, high, indent+4),
		indent)
}
