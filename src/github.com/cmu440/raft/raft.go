//
// raft.go
// =======
// Write your code in this file
// We will use the original version of all other
// files for testing
//

package raft

//
// API
// ===
// This is an outline of the API that your raft implementation should
// expose.
//
// rf = NewPeer(...)
//   Create a new Raft server.
//
// rf.PutCommand(command interface{}) (index, term, isleader)
//   PutCommand agreement on a new log entry
//
// rf.GetState() (me, term, isLeader)
//   Ask a Raft peer for "me", its current term, and whether it thinks it
//   is a leader
//
// ApplyCommand
//   Each time a new entry is committed to the log, each Raft peer
//   should send an ApplyCommand to the service (e.g. tester) on the
//   same server, via the applyCh channel passed to NewPeer()
//

import (
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/cmu440/rpc"
)

// Set to false to disable debug logs completely
// Make sure to set kEnableDebugLogs to false before submitting
const kEnableDebugLogs = false

// Set to true to log to stdout instead of file
const kLogToStdout = true

// Change this to output logs to a different directory
const kLogOutputDir = "./raftlogs/"

// Heartbeat interval  in ms
const heartBeatInterval = 100

//
// ApplyCommand
// ========
//
// As each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyCommand to the service (or
// tester) on the same server, via the applyCh passed to NewPeer()
//
type ApplyCommand struct {
	Index   int
	Command interface{}
}

const (
	leader = iota
	follower
	candidate
)

// type struct for logs
type Log struct {
	//Index   int         // the index for the log
	Term    int         // the term for the log
	Command interface{} // the command stored in the log
}

//
// Raft struct
// ===========
//
// A Go object implementing a single Raft peer
//
type Raft struct {
	mux   sync.Mutex       // Lock to protect shared access to this peer's state
	peers []*rpc.ClientEnd // RPC end points of all peers
	me    int              // this peer's index into peers[]
	// You are expected to create reasonably clear log files before asking a
	// debugging question on Piazza or OH. Use of this logger is optional, and
	// you are free to remove it completely.
	logger *log.Logger // We provide you with a separate logger per peer.

	// Your data here (2A, 2B).
	// Look at the Raft paper's Figure 2 for a description of what
	// state a Raft peer should maintain
	applyCh         chan ApplyCommand // channel for export the channel
	currentTerm     int               // the current term for the raft server
	voteFor         int               // the current candidate id the raft candidate voteFor
	logs            []Log             // log entries
	role            int               // server role status using the enum
	commitIndex     int               // index of the highest log to be committed
	lastApplied     int               // index of the highest log entry applied to the shared machine
	nextIndex       map[int]int       // Leader Usage
	matchIndex      map[int]int       // leader usage
	timeNoRPC       int               // the time for not receiving a heartbeat
	receivedRPC     bool              // received AppendEntry in last epoch
	votedCount      int               // the number of voted elements
	applyingCommand chan ApplyCommand // the channel for queuing the commands
}

//
// GetState()
// ==========
//
// Return "me", current term and whether this peer
// believes it is the leader
//
func (rf *Raft) GetState() (int, int, bool) {
	var me int
	var term int
	var isleader bool
	// Your code here (2A)
	// locks the current rf data and get the status
	rf.mux.Lock()
	me = rf.me
	term = rf.currentTerm
	if rf.role == leader {
		isleader = true
	} else {
		isleader = false
	}
	rf.mux.Unlock()
	return me, term, isleader
}

// Field names must start with capital letters!
// arguements for AppendEntries Arguement
type AppendEntriesArgs struct {
	// Your data here (2A, 2B)
	Term         int   // leader's term
	LeaderId     int   // leader's candidate id
	PrevLogIndex int   //index of the latest log
	PrevLogTerm  int   // index of the
	Entries      []Log // log entries to store
	LeaderCommit int   // lead's commit index
}

// The struct for AppendEntires Reply
type AppendEntriesReply struct {
	Term    int  // currentTerm, for leader to update themselves
	Success bool // true if follower contained entry matching prevLogIndex and prevLogTerm
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	//when a leader receives append entries
	rf.mux.Lock()
	//rf.printAppendEntries(args)
	switch rf.role {
	case leader:
		if rf.currentTerm > args.Term {
			reply.Term = rf.currentTerm
			reply.Success = false
		} else if rf.currentTerm < args.Term { // would there possibly be a equal case?
			rf.convertFromLeaderToFollower(args.Term, -1)
			// is changing vote for needed?
			// rf.voteFor = args.LeaderId
			reply.Term = args.Term
			reply.Success = false
		} else {
			// TODO should handle the case for two leaders with same term?
			reply.Term = args.Term
			lastLogIndex := len(rf.logs) - 1
			if args.PrevLogIndex < len(rf.logs) {
				if args.PrevLogIndex >= lastLogIndex && args.PrevLogTerm >= rf.logs[lastLogIndex].Term {
					rf.convertFromLeaderToFollower(args.Term, -1)
				}
			}
			reply.Success = false
		}
	case candidate:
		if rf.currentTerm <= args.Term {
			rf.convertFromLeaderToFollower(args.Term, -1)
			reply.Term = args.Term
			// Assume the index would not try to check the changes
			reply.Success = false
		} else {
			reply.Term = rf.currentTerm
			reply.Success = false
		}
	case follower:
		// received rpc
		rf.receivedRPC = true

		if rf.currentTerm > args.Term { // reject appendEntries
			reply.Term = rf.currentTerm
			reply.Success = false
		} else {
			if rf.currentTerm < args.Term {
				rf.currentTerm = args.Term
				rf.voteFor = -1
				reply.Term = args.Term
				reply.Success = false
			} else if rf.currentTerm == args.Term {
				reply.Term = args.Term
				reply.Success = false
			}
		}
	}
	// this is the case should check with last log index and consider return true or false
	if rf.role == follower && reply.Success == false {
		prevLogIndex := args.PrevLogIndex
		prevLogTerm := args.PrevLogTerm
		logLength := len(rf.logs) - 1  // the length should be minus 1 to match the index
		if prevLogIndex <= logLength { // comparing the length
			if rf.logs[prevLogIndex].Term == prevLogTerm {
				// when a match happens
				reply.Success = true
				// this case for appending
				if len(args.Entries) > 0 {
					rf.logs = rf.logs[0 : prevLogIndex+1] // up until the prevLogIndex
					for _, item := range args.Entries {   // append the new logs to the follower logs
						rf.logs = append(rf.logs, item)
					}
				}
				// the case for checking commitIndex after appending
				commitIndex := args.LeaderCommit
				if commitIndex <= logLength && commitIndex != rf.lastApplied { // check it with the length
					rf.commitIndex = commitIndex
					// updates the applyChan
					for i := rf.lastApplied + 1; i <= rf.commitIndex; i++ {
						if i != 0 {
							command := rf.logs[i].Command
							rf.logger.Printf("Follower %v sends to client, command %v, command Index %v",
								rf.me, command, i)
							rf.applyingCommand <- ApplyCommand{Index: i, Command: command}
						}
					}
					rf.lastApplied = rf.commitIndex
				}
			}
		}
	}
	rf.printCurrentRaftInfo()
	rf.mux.Unlock()
}

//
// RequestVoteArgs
// ===============
//
// Example RequestVote RPC arguments structure
//
// Please note
// ===========
// Field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B)
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

//
// RequestVoteReply
// ================
//
// Example RequestVote RPC reply structure.
//
// Please note
// ===========
// Field names must start with capital letters!
//
//
type RequestVoteReply struct {
	// Your data here (2A)
	Term        int
	VoteGranted bool
}

//
// RequestVote
// ===========
//
// Example RequestVote RPC handler
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B)
	rf.mux.Lock()
	rf.printReceivedRequestVote(args)
	rf.receivedRPC = true
	if rf.currentTerm > args.Term {
		reply.Term = rf.currentTerm
		reply.VoteGranted = false

	} else {
		if rf.currentTerm < args.Term {
			// when the term
			rf.convertFromLeaderToFollower(args.Term, -1)
			//rf.currentTerm = args.Term
			//rf.voteFor = -1
		}
		reply.Term = args.Term
		reply.VoteGranted = false
		// would a leader receive a request vote with higher term?
		if rf.voteFor == -1 || rf.voteFor == args.CandidateId {
			// then check the latest log index and term
			currentLastIndex := len(rf.logs) - 1
			currentLastTerm := rf.logs[currentLastIndex].Term
			if args.LastLogTerm > currentLastTerm {
				reply.VoteGranted = true
			} else if args.LastLogTerm == currentLastTerm {
				if args.LastLogIndex >= currentLastIndex {
					reply.VoteGranted = true
				}
			}
		}
	}
	rf.logger.Printf("Id %d CurrentTerm:%v replied RequestVote  term:%v, grantVoted:%v, "+
		"currentLastIndex %v, currentLastTerm %v",
		rf.me, rf.currentTerm, reply.Term, reply.VoteGranted, len(rf.logs)-1, rf.logs[len(rf.logs)-1].Term)
	rf.mux.Unlock()
}

//
// sendRequestVote
// ===============
//
// Example code to send a RequestVote RPC to a server
//
// server int -- index of the target server in
// rf.peers[]
//
// args *RequestVoteArgs -- RPC arguments in args
//
// reply *RequestVoteReply -- RPC reply
//
// The types of args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers)
//
// The rpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost
//
// Call() sends a request and waits for a reply
//
// If a reply arrives within a timeout interval, Call() returns true;
// otherwise Call() returns false
//
// Thus Call() may not return for a while
//
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply
//
// Call() is guaranteed to return (perhaps after a delay)
// *except* if the handler function on the server side does not return
//
// Thus there
// is no need to implement your own timeouts around Call()
//
// Please look at the comments and documentation in ../rpc/rpc.go
// for more details
//
// If you are having trouble getting RPC to work, check that you have
// capitalized all field names in the struct passed over RPC, and
// that the caller passes the address of the reply struct with "&",
// not the struct itself
//
func (rf *Raft) sendRequestVote(index int, args *RequestVoteArgs, reply *RequestVoteReply) {
	clientEnd := rf.peers[index]
	ok := clientEnd.Call("Raft.RequestVote", args, reply)
	// reply was received
	rf.mux.Lock()
	// if rf is still a candidate, keeps changing the count or term
	// if rf was transferred to a server or to a follower do not care about the output anymore?

	if ok {
		rf.printRequestVote(index, reply)
		// updates the votedCount
		if reply.VoteGranted {
			if rf.role == candidate {
				rf.votedCount = rf.votedCount + 1
				if rf.votedCount*2 > len(rf.peers) {
					// it becomes the leader
					// with
					rf.initializeLeader()
					rf.logger.Printf("candidate id%v becomes leader", rf.me)
					// then the go routine will handle the events
				}
			}
		} else {
			// If candidate receives a false with a higher
			if reply.Term > rf.currentTerm {
				if rf.role != follower {
					rf.convertFromLeaderToFollower(reply.Term, -1)
				} else {
					rf.currentTerm = reply.Term
				}
			}
		}
	}
	rf.mux.Unlock()
}

//
// PutCommand
// =====
//
// The service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log
//
// If this server is not the leader, return false
//
// Otherwise start the agreement and return immediately
//
// There is no guarantee that this command will ever be committed to
// the Raft log, since the leader may fail or lose an election
//
// The first return value is the index that the command will appear at
// if it is ever committed
//
// The second return value is the current term
//
// The third return value is true if this server believes it is
// the leader
//
func (rf *Raft) PutCommand(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true
	// Your code here (2B)
	rf.mux.Lock()
	if rf.role == leader {
		term = rf.currentTerm
		index = len(rf.logs) // index = length
		// whether first index should be 1 or not
		rf.logs = append(rf.logs, Log{
			term,
			command,
		})
		rf.logger.Printf("ID:%v Command %v append, Current Log length %v", rf.me, command, len(rf.logs))
	} else {
		isLeader = false
	}
	rf.mux.Unlock()

	return index, term, isLeader
}

//
// Stop
// ====
//
// The tester calls Stop() when a Raft instance will not
// be needed again
//
// You are not required to do anything
// in Stop(), but it might be convenient to (for example)
// turn off debug output from this instance
//
func (rf *Raft) Stop() {
	// Your code here, if desired
}

// doing the election timeout
// if it is leader it also needs to send out the heartbeat
func (rf *Raft) timerActions() {
	electionTimeOut := newRandomTimeout()
	time.Sleep(time.Millisecond * electionTimeOut)
	// follower receives rpc for received entry\
	// candidate receives AppendEntries to become followers
	for {
		leaderElection := false // the variable for controlling the leader election
		rf.mux.Lock()
		if rf.role == candidate {
			leaderElection = true
		} else if rf.role == follower {
			if rf.receivedRPC == false {
				// starts voting
				leaderElection = true
			} else {
				rf.receivedRPC = false // reset the receivedRPC to false
			}
		}
		// starts the voting process
		// before the next timeout, the node should be become a leader or a follower or starts a new
		if leaderElection == true {
			rf.role = candidate
			rf.currentTerm = rf.currentTerm + 1
			rf.voteFor = rf.me
			rf.votedCount = 1 // voted for oneself
			// send RPC request to every other
			rf.printLeaderElection()
			for index, _ := range rf.peers {
				if index != rf.me {
					args := &RequestVoteArgs{}
					reply := &RequestVoteReply{}
					// assigning values for arguments
					args.Term = rf.currentTerm
					args.CandidateId = rf.me
					var sendingIndex int
					var sendingTerm int

					sendingIndex = len(rf.logs) - 1
					sendingTerm = rf.logs[sendingIndex].Term

					args.LastLogTerm = sendingTerm
					args.LastLogIndex = sendingIndex
					go rf.sendRequestVote(index, args, reply)
				}
			}
		}

		rf.mux.Unlock()
		electionTimeOut = newRandomTimeout()
		time.Sleep(time.Millisecond * electionTimeOut)
	}
}

// thr function that will control the go routine for leader heartbeat
func (rf *Raft) leaderHeartBeats() {
	for {
		var me int
		rf.mux.Lock()
		if rf.role == leader {
			me = rf.me
			// send heartbeat
			for index, _ := range rf.peers {
				if index != me { // not sending to itself
					// send heartbeat
					args := &AppendEntriesArgs{}
					args.LeaderId = me
					args.Term = rf.currentTerm
					//args.Entries = []Log{} // empty logs for heartbeat
					args.LeaderCommit = rf.commitIndex

					// get the nextIndex and term
					// When the logs are empty what should I pass
					nextIndex := rf.nextIndex[index]
					var sendingIndex int
					var sendingTerm int

					if nextIndex == 0 {
						sendingIndex = 0
					} else {
						sendingIndex = nextIndex - 1
					}
					//if sendingIndex < len(rf.logs)-1 {
					//	sendingIndex = len(rf.logs) - 1
					//}
					sendingTerm = rf.logs[sendingIndex].Term
					args.PrevLogIndex = sendingIndex
					args.PrevLogTerm = sendingTerm
					reply := &AppendEntriesReply{}
					// send heartbeat to each of them parallel
					rf.printSendAppendEntries(index, args)
					go rf.sendHeartBeat(index, args, reply)
				}
			}
		}
		rf.mux.Unlock()                                  // Unlock at the end
		time.Sleep(time.Millisecond * heartBeatInterval) // sleep for the heartbeat interval
	}
}

// sending the heartbeats and later sends the actual appendEntry
func (rf *Raft) sendHeartBeat(index int, args *AppendEntriesArgs, reply *AppendEntriesReply) {
	clientEnd := rf.peers[index]
	callResult := clientEnd.Call("Raft.AppendEntries", args, reply)
	sendData := false
	newArgs := &AppendEntriesArgs{}
	newReply := &AppendEntriesReply{}
	// logic for dealing with logs
	if callResult == true {
		// heartbeat succeed
		// check the reply result
		rf.mux.Lock()
		//rf.logger.Printf("%v Result from %v, %v", rf.me, index, reply.Success)
		if rf.currentTerm < reply.Term {
			rf.convertFromLeaderToFollower(reply.Term, -1)
		} else {
			if rf.role == leader {
				if reply.Success == true {
					// update match index
					if len(args.Entries) == 0 { // means it is a heartbeat, will try to activat
						// should I update the match index here?
						if rf.nextIndex[index] < len(rf.logs) {
							newArgs.Entries = rf.logs[rf.nextIndex[index]:] // get the needed slices
							if len(newArgs.Entries) != 0 {
								sendData = true
								newArgs.Term = rf.currentTerm
								newArgs.LeaderId = rf.me
								newArgs.LeaderCommit = rf.commitIndex
								newArgs.PrevLogIndex = args.PrevLogIndex
								newArgs.PrevLogTerm = args.PrevLogTerm
							}
						}
					} else {
						// only when the follower appends the logs as required
						// the nextIndex and matchIndex starts to change

						rf.matchIndex[index] = len(rf.logs) - 1 // minus 1 as match index is preceding log of the nextIndex
						rf.nextIndex[index] = len(rf.logs)      // should it also be -1 to get the

						// now should be the time to update the commit index
						//rf.logger.Printf("before update commit")
						rf.updateCommit()
						//rf.logger.Printf("After update commit")
						// TODO after updating commitIndex check it with last applied
						commitIndex := rf.commitIndex
						logLength := len(rf.logs) - 1
						if commitIndex <= logLength && commitIndex != rf.lastApplied { // check it with the length why is this needed?
							rf.commitIndex = commitIndex
							// updates the applyChan

							for i := rf.lastApplied + 1; i <= rf.commitIndex; i++ {
								if i != 0 {
									command := rf.logs[i].Command
									rf.logger.Printf("Leader %v sends to client, command %v, command Index %v",
										rf.me, command, i)
									rf.applyingCommand <- ApplyCommand{Index: i, Command: command}
								}
							}
							rf.lastApplied = rf.commitIndex
						}
					}
				} else {
					nextIndex := rf.nextIndex[index]
					if nextIndex >= 1 {
						rf.nextIndex[index] = nextIndex - 1
					}
				}
			} else {
				// when it is not leader do nothing
			}
		}

		if sendData {
			// send the actual entries
			rf.printSendAppendEntries(index, newArgs)
			rf.mux.Unlock()
			go rf.sendHeartBeat(index, newArgs, newReply)
		} else {
			rf.mux.Unlock()
		}
	} else {
		// current do nothing when the heartbeat call failed
	}
}

// this function would only be called within a mutex lock range
// so it does need a mutex inside for the raft

func (rf *Raft) convertFromLeaderToFollower(term int, voteFor int) {
	// rf.logger.Printf("Id %v convert to follower", rf.me)
	if rf.role != follower {
		rf.role = follower
	}
	if rf.role == candidate {
		rf.votedCount = 0
	}
	rf.voteFor = voteFor
	rf.receivedRPC = true
	rf.currentTerm = term
}

// this function would only be called within a mutex lock range
// so it does need a mutex inside for the raft
func (rf *Raft) initializeLeader() {
	// make a different lock Maybe?
	rf.role = leader
	// initialize the nextIndex, matchIndex
	rf.nextIndex = make(map[int]int)
	rf.matchIndex = make(map[int]int)
	for index, _ := range rf.peers {
		if index != rf.me { //myself should not be included
			rf.nextIndex[index] = len(rf.logs) // when it is empty, what should the next index be 0
			rf.matchIndex[index] = 0
		}
	}
}

/*
	assume the rf will be locked to use the function.
	this function will check on whether to update commit index based on would majority of the
 	followers has a match index that is larger or equal to the commit index
	and when the commitIndex < the length of logs -1
	when commitIndex == len(rf.logs)-1, it means logs are all up to date
*/
func (rf *Raft) updateCommit() {
	count := 2
	majority := len(rf.peers) // assume myself is in the peers
	stopChecking := false
	if rf.commitIndex < len(rf.logs)-1 {
		for stopChecking == false {
			for _, element := range rf.matchIndex {
				if element > rf.commitIndex {
					count = count + 2
				}
			}
			if count > majority {
				if rf.commitIndex == len(rf.logs)-1 {
					// when they equals do not increase
					stopChecking = true
				} else {
					rf.commitIndex = rf.commitIndex + 1
				}
			} else {
				stopChecking = true
			}
		}
	}
}

func (rf *Raft) sendApplyCh() {
	for {
		select {
		case cmd := <-rf.applyingCommand:
			rf.applyCh <- cmd
			rf.logger.Printf("%v send command %v %v", rf.me, cmd.Index, cmd.Command)
		}
	}
}
func newRandomTimeout() time.Duration {
	return time.Duration(100) + time.Duration(rand.Intn(300))
}

func (rf *Raft) printLeaderElection() {
	rf.logger.Printf("Leader election starts: Id:%v Term:%v Votefor: %v", rf.me, rf.currentTerm, rf.voteFor)
}

func (rf *Raft) printCurrentRaftInfo() {
	rf.logger.Printf("Raft id: %v, term: %v, log length %v, commitIndex: %v, lastApplied: %v",
		rf.me, rf.currentTerm, len(rf.logs), rf.commitIndex, rf.lastApplied)
}

func (rf *Raft) printSendAppendEntries(index int, args *AppendEntriesArgs) {
	rf.logger.Printf("ID: %v sending AppendEntries to %v, "+
		"prevIndex: %v, prevTerm: %v, leaderCommit: %v, length Entries %v, nextIndex: %v",
		rf.me, index, args.PrevLogIndex, args.PrevLogTerm, args.LeaderCommit, len(args.Entries), rf.nextIndex[index])
}
func (rf *Raft) printAppendEntries(args *AppendEntriesArgs) {
	if args == nil {
		rf.logger.Println("Args are null")
	}
	rf.logger.Printf("Id:%v CurrentTerm: %v get AE from id:%v, term:%v, prevLogIndex:%v, prevLogTerm: %v",
		rf.me, rf.currentTerm, args.LeaderId, args.Term, args.PrevLogIndex, args.PrevLogTerm)
}
func (rf *Raft) printRequestVote(index int, reply *RequestVoteReply) {

	roleString := "No role"
	switch rf.role {
	case leader:
		roleString = "leader"
	case candidate:
		roleString = "candidate"
	case follower:
		roleString = "follower"
	}

	rf.logger.Printf("id:%v %v Received RV reply from Id:%v with Term:%v, voteGranted %v",
		rf.me, roleString, index, reply.Term, reply.VoteGranted)

}
func (rf *Raft) printReceivedRequestVote(args *RequestVoteArgs) {

	roleString := "No role"
	switch rf.role {
	case leader:
		roleString = "leader"
	case candidate:
		roleString = "candidate"
	case follower:
		roleString = "follower"
	}
	rf.logger.Printf("Id %d %v CurrentTerm:%v received RequestVote term:%v, candidateId:%v, lastLogIndex %v, lastLogTerm %v, voteFor %v",
		rf.me, roleString, rf.currentTerm, args.Term, args.CandidateId, args.LastLogIndex, args.LastLogTerm, rf.voteFor)
}

//
// NewPeer
// ====
//
// The service or tester wants to create a Raft server
//
// The port numbers of all the Raft servers (including this one)
// are in peers[]
//
// This server's port is peers[me]
//
// All the servers' peers[] arrays have the same order
//
// applyCh
// =======
//
// applyCh is a channel on which the tester or service expects
// Raft to send ApplyCommand messages
//
// NewPeer() must return quickly, so it should start Goroutines
// for any long-running work
//
func NewPeer(peers []*rpc.ClientEnd, me int, applyCh chan ApplyCommand) *Raft {
	// nobody will use the
	rf := &Raft{}
	rf.peers = peers
	rf.me = me
	rf.applyCh = applyCh
	if kEnableDebugLogs {
		peerName := peers[me].String()
		logPrefix := fmt.Sprintf("%s ", peerName)
		if kLogToStdout {
			rf.logger = log.New(os.Stdout, strconv.FormatInt(int64(me), 10), log.Lshortfile)
		} else {
			err := os.MkdirAll(kLogOutputDir, os.ModePerm)
			if err != nil {
				panic(err.Error())
			}
			logOutputFile, err := os.OpenFile(fmt.Sprintf("%s/%s.txt", kLogOutputDir, logPrefix), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
			if err != nil {
				panic(err.Error())
			}
			rf.logger = log.New(logOutputFile, logPrefix, log.Lmicroseconds|log.Lshortfile)
		}
		rf.logger.Println("logger initialized")
	} else {
		rf.logger = log.New(ioutil.Discard, "", 0)
	}

	// Your initialization code here (2A, 2B)
	rf.role = follower // first initialize the candidate to leader
	rf.currentTerm = 0
	rf.voteFor = -1 // -1 represent the nil case for not voted yet
	rf.logs = make([]Log, 1)
	rf.commitIndex = 0
	rf.lastApplied = 0
	rf.timeNoRPC = 0
	rf.votedCount = 0
	rf.applyingCommand = make(chan ApplyCommand, 1000) // first assume queueing 1000 commands should be fine
	// starts the routine for the sending messages or election timeout
	go rf.timerActions()
	go rf.leaderHeartBeats()
	go rf.sendApplyCh()
	return rf
}
