package raft

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

// import "fmt"
// import "time"
// import "sync/atomic"
import "../labrpc"

// import "bytes"
// import "../labgob"

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in Lab 3 you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh; at that point you can add fields to
// ApplyMsg, but set CommandValid to false for these other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
}

type Entry struct {
	Command interface{}
	Term    int
}

// timeout settings (ms)
const (
	MaxTimeout int = 1000
	MinTimeout int = 500
)

// indicate Raft node's role
const (
	LEADER    = 1
	FOLLOWER  = 2
	CANDIDATE = 3
)

const NonVote = -1

type raftLog struct {
	lastIndex int
	lastTerm int
	commit int
	entries []Entry
}

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	term int
	votedFor int

	role int

	rl raftLog

	votes map[int]struct{}

	ticker *time.Ticker
	tick func()

	electionElapsed  int
	heartbeatElapsed int

	electionTimeout  int
	heartbeatTimeout int

	// handler
	handleRequestVote        func(args *RequestVoteArgs, reply *RequestVoteReply)
	handleRequestVoteReply   func(reply *RequestVoteReply)
	handleAppendEntries      func(args *AppendEntriesArgs, reply *AppendEntriesReply)
	handleAppendEntriesReply func(reply *AppendEntriesReply)
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	// Your code here (2A).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	term, isLeader := rf.term, rf.role == LEADER
	return term, isLeader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term int
	CandidateID int
	LastLogIndex int
	LastLogTerm int
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int
	From		int
	VoteGranted bool
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	rf.handleRequestVote(args, reply)
	rf.mu.Unlock()
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendAndHandleRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) {
	if ok := rf.sendRequestVote(server, args, reply); ok {
		rf.mu.Lock()
		rf.handleRequestVoteReply(reply)
		rf.mu.Unlock()
	}
}

// AppendEntries RPC
type AppendEntriesArgs struct {
}

type AppendEntriesReply struct {
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	rf.handleAppendEntries(args, reply)
	rf.mu.Unlock()
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	// DPrintf("node%d send heartbreak to node%d", rf.me, server)
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

func (rf *Raft) sendAndHandleAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) {
	if ok := rf.sendAppendEntries(server, args, reply); ok {
		rf.mu.Lock()
		rf.handleAppendEntriesReply(reply)
		rf.mu.Unlock()
	}
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	// Your code here (2B).

	return 0, 0, false
}

//
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
	// DPrintf("node%d has been killed.\n", rf.me)
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

/* ---------- Tick Function ----------*/

func (rf *Raft) tickElection() {
	rf.electionElapsed++
	if rf.electionElapsed == rf.electionTimeout {
		rf.term++
		rf.becomeCandidate()
	}
}

func (rf *Raft) tickHeartbeat() {
	rf.heartbeatElapsed++
	if rf.heartbeatElapsed == rf.heartbeatTimeout {
		rf.heartbeatElapsed = 0
		// send append entries rpc
		for i, _ := range rf.peers {
			if i == rf.me {
				continue
			}
			go func(id int) {
				// todo
				args := &AppendEntriesArgs{}
				reply := &AppendEntriesReply{}
				rf.sendAndHandleAppendEntries(id, args, reply)
			}(i)
		}
	}
}

/* ---------- Machine State Transition ---------- */

func (rf *Raft) becomeFollower() {
	rf.role = FOLLOWER
	rf.votedFor = NonVote

	rf.tick = rf.tickElection
	rf.electionElapsed = 0

	rf.handleRequestVote = rf.commonHandleRequestVote
	rf.handleAppendEntries = rf.followerHandleAppendEntries
}

func (rf *Raft) becomeCandidate() {
	rf.role = CANDIDATE
	rf.votedFor = rf.me

	rf.tick = rf.tickElection
	rf.electionElapsed = 0

	rf.handleRequestVoteReply = rf.candidateHandleRequestVoteReply

	// clear votes log and vote self
	rf.votes = make(map[int]struct{})
	rf.votes[rf.me] = struct{}{}

	// send request vote rpc
	for i, _ := range rf.peers {
		if i == rf.me {
			continue
		}
		go func(id int) {
			args := &RequestVoteArgs{
				Term:         rf.term,
				CandidateID:  rf.me,
				LastLogIndex: rf.rl.lastIndex,
				LastLogTerm:  rf.rl.lastTerm,
			}

			reply := &RequestVoteReply{}
			rf.sendAndHandleRequestVote(id, args, reply)
		}(i)
	}
}

func (rf *Raft) becomeLeader() {
	rf.role = LEADER

	rf.tick = rf.tickHeartbeat
	rf.heartbeatElapsed = 0

	rf.handleAppendEntriesReply = rf.leaderHandleAppendEntriesReply
}

/* --------- Request Vote RPC Handler ---------- */

// TODO: Can the three state use a common handler?
func (rf *Raft) commonHandleRequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	reply.Term = rf.term
	reply.From = rf.me

	// TODO: simplify the vote logic
	if rf.term > args.Term || (rf.term == args.Term && rf.votedFor != NonVote) ||
		(args.LastLogTerm < rf.rl.lastTerm || args.LastLogIndex < rf.rl.lastIndex){
		reply.VoteGranted = false
		return
	}

	rf.term = args.Term
	rf.votedFor = args.CandidateID

	reply.VoteGranted = true

	rf.becomeFollower()
}

func (rf *Raft) RequestVoteCandidateHandler(args *RequestVoteArgs, reply *RequestVoteReply) {

}

/* ---------- Request Vote RPC Reply Handler ---------- */

func (rf *Raft) candidateHandleRequestVoteReply(reply *RequestVoteReply) {
	if reply.Term > rf.term {
		rf.term = reply.Term
		rf.becomeFollower()
		return
	}

	if !reply.VoteGranted{
		return
	}

	rf.votes[reply.From] = struct{}{}
	if len(rf.votes) >= (1 + len(rf.peers)/2) {
		rf.becomeLeader()
	}
}

/* ---------- Append Entries RPC Handler ---------- */

func (rf *Raft) followerHandleAppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.electionElapsed = 0
}

/* ---------- Append Entries Reply RPC Handler ---------- */

func (rf *Raft) leaderHandleAppendEntriesReply(reply *AppendEntriesReply) {

}

func (rf *Raft) run() {
	for {
		<-rf.ticker.C
		rf.mu.Lock()
		rf.tick()
		rf.mu.Unlock()
	}
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//

func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).
	rf.ticker = time.NewTicker(time.Millisecond)
	rf.heartbeatTimeout = 5
	rf.electionTimeout = 5 + rand.Intn(10)

	rf.becomeFollower()
	go rf.run()

	return rf
}
