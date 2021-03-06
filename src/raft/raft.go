package raft

import (
	"../labgob"
	"../labrpc"
	"bytes"
	"log"
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

// ApplyMsg
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

// indicate Raft node's role
const (
	Leader = iota
	Follower
	Candidate
	PreCandidate
)

const NonVote = -1

//
// raft log implementation
//
type raftLog struct {
	off     int
	commit  int
	apply   int
	entries []Entry
}

func (rl *raftLog) SetEntries(ents []Entry) {
	rl.entries = ents
}

func (rl *raftLog) Append(ents []Entry) {
	rl.entries = append(rl.entries, ents...)
}

func (rl *raftLog) DeleteFrom(from int) {
	if from <= rl.commit {
		log.Fatalf("delete from invalid index")
	}
	rl.entries = rl.entries[:from]
}

func (rl *raftLog) Entry(i int) Entry {
  return rl.entries[i - rl.off]
}

func (rl *raftLog) Entries() []Entry {
	return rl.entries
}

func (rl *raftLog) CommitIndex() int {
	return rl.commit
}

func (rl *raftLog) CommitTo(ci int) bool {
	if ci <= rl.commit {
		return false
	}
	rl.commit = ci
	return true
}

func (rl *raftLog) ApplyIndex() int {
	return rl.apply
}

func (rl *raftLog) ApplyTo(ai int) bool {
	if ai <= rl.apply {
		return false
	}
	rl.apply = ai
	return true
}

func (rl *raftLog) Match(index, term int) bool {
	if rl.LastIndex() < index ||
		rl.entries[index].Term != term {
		return false
	}
	return true
}

func (rl *raftLog) IsUpToDate(index, term int) bool {
	if term < rl.LastTerm() {
		return false
	}
	if term == rl.LastTerm() &&
		index < rl.LastIndex() {
		return false
	}
	return true
}

// take the copy of n entries from raft log
func (rl *raftLog) Take(begin, n int) []Entry {
	end := min(begin+n, rl.LastIndex()+1)
	n = end - begin
	ent := make([]Entry, n)
	copy(ent, rl.entries[begin:end])
	return ent
}

// find the possible match index of two logs
func (rl *raftLog) FindHint(term, index int) int {
	i, li := index, rl.LastIndex()
	if index > li {
		i = li
	}
	for ; i >= 0; i-- {
		if rl.entries[i].Term <= term {
			break
		}
	}
	return i
}

func (rl *raftLog) LastIndex() int {
	return rl.off + len(rl.entries) - 1
}

func (rl *raftLog) LastTerm() int {
	return rl.entries[len(rl.entries)-1].Term
}

func newRaftLog() *raftLog {
	rl := &raftLog{}
	rl.entries = make([]Entry, 1)
	rl.entries[0] = Entry{struct{}{}, 0}
	return rl
}

type Stable struct {
	Term     int
	VotedFor int
	Commit   int
	Applied  int
	Entries  []Entry
}

func (rf *Raft) makeStable() Stable {
	return Stable{
		Term:     rf.Term,
		VotedFor: rf.VotedFor,
		Commit:   rf.log.CommitIndex(),
		Applied:  rf.log.ApplyIndex(),
		Entries:  rf.log.Entries(),
	}
}

//
// A progress struct represent a peer's progress of log replication
// from the leader's view
//
type progress struct {
	match int
	next  int
	state int
}

const (
	StateProbe = iota
	StateReplicate
	StateSnapshot
)

//
// progress tracker for leader to trace the progress of log replication
//
type progressTracer []*progress

func makeProgressTracer(n int) progressTracer {
	pt := make(progressTracer, n)
	return pt
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

	// state need to persist
	Term     int
	VotedFor int
	log      *raftLog
	snapshot *raftSnapshot

	role    int
	pt      progressTracer
	votes   map[int]struct{}
	applyCh chan ApplyMsg
	tick    func()

	electionElapsed   int
	heartbeatElapsed  int
	electionTimeout   int
	heartbeatTimeout  int
	maxNumSentEntries int // max number of entries to be sent in one rpc
}

// GetState return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	// Your code here (2A).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	term, isLeader := rf.Term, rf.role == Leader
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

	stable := rf.makeStable()

	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	if err := e.Encode(stable); err != nil {
		log.Fatal(err)
	}
	data := w.Bytes()
	rf.persister.SaveRaftState(data)
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

	s := Stable{}
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	if err := d.Decode(&s); err != nil {
		log.Fatal("readPersist: ", err)
	}
	rf.Term, rf.VotedFor = s.Term, s.VotedFor
	rf.log.SetEntries(s.Entries)
	rf.log.CommitTo(s.Commit)
	rf.apply(1, s.Commit)
}

// RequestVoteArgs
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term         int
	CandidateID  int
	LastLogIndex int
	LastLogTerm  int
	PreVote      bool
}

// RequestVoteReply
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term         int
	From         int
	VoteGranted  bool
	RejectReason string
}

// RequestVote
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	rf.handleRequestVote(args, reply)
	rf.persist()
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
		rf.handleRequestVoteReply(args, reply)
		rf.persist()
		rf.mu.Unlock()
	}
}

// AppendEntries RPC
type AppendEntriesArgs struct {
	Term         int
	LeaderID     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []Entry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term      int
	From      int
	HintIndex int
	HintTerm  int
	Success   bool
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	rf.handleAppendEntries(args, reply)
	rf.persist()
	rf.mu.Unlock()
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

func (rf *Raft) sendAndHandleAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) (ok bool) {
	if ok = rf.sendAppendEntries(server, args, reply); ok {
		rf.mu.Lock()
		rf.handleAppendEntriesReply(args, reply)
		rf.persist()
		rf.mu.Unlock()
	}
	return ok
}

func (rf *Raft) CheckQuorum() bool {
	if _, isLead := rf.GetState(); !isLead {
		return false
	}
	// send heartbeat
	// TODO: reduce duplicate code
	resp := make(chan bool, len(rf.peers))
	for i, _ := range rf.peers {
		if i == rf.me {
			continue
		}
		// no update, send heartbeat
		pg := rf.pt[i]
		ni := pg.next
		prevLogIndex := ni - 1
		prevLogTerm := rf.log.Entry(prevLogIndex).Term
		// prepare entries
		var ents []Entry
		switch pg.state {
		case StateProbe:
			ents = rf.log.Take(ni, 1)
		case StateReplicate:
			ents = rf.log.Take(ni, rf.maxNumSentEntries)
		}
		args := &AppendEntriesArgs{
			Term:         rf.Term,
			LeaderID:     rf.me,
			PrevLogIndex: prevLogIndex,
			PrevLogTerm:  prevLogTerm,
			Entries:      ents,
			LeaderCommit: rf.log.CommitIndex(),
		}
		go func(i int) {
			reply := &AppendEntriesReply{}
			ok := rf.sendAndHandleAppendEntries(i, args, reply)
			ok = ok && (reply.Term <= rf.Term)
			resp <- ok
		}(i)
	}
	t, ack := time.NewTimer(time.Second), 1
	defer t.Stop()
	for {
		select {
		case ok := <-resp:
			if ok {
				ack++
			}
			if ack >= len(rf.peers)/2+1 {
				return true
			}
		case <-t.C:
			return false
		}
	}
}

type raftSnapshot struct {
	lastIncludedIndex int
	lastIncludedTerm  int
	data              []byte
}

func (rf *Raft) Snapshot(index int, snapshot []byte) {
  if index > rf.snapshot.lastIncludedIndex {
  	rf.snapshot.lastIncludedIndex = index
  	rf.snapshot.lastIncludedTerm = rf.log.entries[index].Term
  	rf.snapshot.data = snapshot
  	// compact log
  	rf.log.off = index+1
  	rf.log.entries = rf.log.entries[rf.log.off:]
	}
}

func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int,
	lastIncludedIndex int, snapshot []byte) bool {
	return true
}

// Start
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
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()
	if rf.role != Leader {
		return 0, 0, false
	} else {
		index, term := rf.log.LastIndex()+1, rf.Term
		DPrintf("[debug prop] %d receive propose %v index %d term %d\n", rf.me, command, index, term)
		rf.propose(command)
		return index, term, true
	}
}

// Kill
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
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

//
// tick functions
//
func (rf *Raft) tickElection() {
	rf.electionElapsed++
	if rf.electionElapsed == rf.electionTimeout {
		switch rf.role {
		case Follower:
			rf.becomePreCandidate()
		case PreCandidate:
			rf.becomeFollower(rf.Term, rf.VotedFor)
		}
	}
}

func (rf *Raft) tickHeartbeat() {
	rf.heartbeatElapsed++
	if rf.heartbeatElapsed == rf.heartbeatTimeout {
		rf.heartbeatElapsed = 0
		rf.broadcastHeartbeat()
	}
}

//
// Machine state transition
//
func (rf *Raft) becomeFollower(term, votedFor int) {
	DPrintf("[debug node] %d become follower in term %d\n", rf.me, term)
	rf.role = Follower
	rf.Term = term
	rf.VotedFor = votedFor
	rf.tick = rf.tickElection
	rf.electionElapsed = 0
}

func (rf *Raft) becomePreCandidate() {
	DPrintf("[debug node] %d become pre candidate in term %d\n", rf.me, rf.Term)
	rf.role = PreCandidate
	rf.tick = rf.tickElection
	rf.electionElapsed = 0
	// clear votes log and vote self
	rf.votes = make(map[int]struct{})
	rf.votes[rf.me] = struct{}{}
	// send request vote rpc
	for i, _ := range rf.peers {
		if i == rf.me {
			continue
		}
		args := &RequestVoteArgs{
			Term:         rf.Term + 1,
			CandidateID:  rf.me,
			LastLogIndex: rf.log.LastIndex(),
			LastLogTerm:  rf.log.LastTerm(),
			PreVote:      true,
		}
		go rf.sendAndHandleRequestVote(i, args, &RequestVoteReply{})
	}
}

func (rf *Raft) becomeCandidate() {
	DPrintf("[debug node] %d become candidate in term %d\n", rf.me, rf.Term)
	rf.role = Candidate
	rf.VotedFor = rf.me
	rf.tick = rf.tickElection
	rf.electionElapsed = 0
	// clear votes log and vote self
	rf.votes = make(map[int]struct{})
	rf.votes[rf.me] = struct{}{}
	// send request vote rpc
	for i, _ := range rf.peers {
		if i == rf.me {
			continue
		}
		args := &RequestVoteArgs{
			Term:         rf.Term,
			CandidateID:  rf.me,
			LastLogIndex: rf.log.LastIndex(),
			LastLogTerm:  rf.log.LastTerm(),
			PreVote:      false,
		}
		go rf.sendAndHandleRequestVote(i, args, &RequestVoteReply{})
	}
}

func (rf *Raft) resetProgressTracker() {
	pt := makeProgressTracer(len(rf.peers))
	for i, _ := range pt {
		if i == rf.me {
			pt[i] = &progress{match: rf.log.LastIndex()}
			continue
		}
		pt[i] = &progress{
			match: 0,
			next:  rf.log.LastIndex() + 1,
		}
		pt[i].state = StateProbe
	}
	rf.pt = pt
}

func (rf *Raft) broadcastHeartbeat() {
	for i, _ := range rf.peers {
		if i == rf.me {
			continue
		}
		// no update, send heartbeat
		pg := rf.pt[i]
		ni := pg.next
		prevLogIndex := ni - 1
		prevLogTerm := rf.log.entries[prevLogIndex].Term
		// prepare entries
		var ents []Entry
		switch pg.state {
		case StateProbe:
			ents = rf.log.Take(ni, 1)
		case StateReplicate:
			ents = rf.log.Take(ni, rf.maxNumSentEntries)
		}
		args := &AppendEntriesArgs{
			Term:         rf.Term,
			LeaderID:     rf.me,
			PrevLogIndex: prevLogIndex,
			PrevLogTerm:  prevLogTerm,
			Entries:      ents,
			LeaderCommit: rf.log.CommitIndex(),
		}
		go rf.sendAndHandleAppendEntries(i, args, &AppendEntriesReply{})
	}
}

func (rf *Raft) becomeLeader() {
	DPrintf("[debug node] %d become leader in term %d\n", rf.me, rf.Term)
	rf.role = Leader
	rf.tick = rf.tickHeartbeat
	rf.heartbeatElapsed = 0
	rf.resetProgressTracker()
	rf.broadcastHeartbeat()
}

//
// Request Vote RPC handler
//
func (rf *Raft) handleRequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	reply.Term = rf.Term
	reply.From = rf.me
	if rf.Term > args.Term {
		reply.VoteGranted = false
		reply.RejectReason = "receiver has greater term"
		return
	}
	// only check whether log is up-to-date for pre vote
	if args.PreVote {
		reply.VoteGranted = rf.log.IsUpToDate(args.LastLogIndex, args.LastLogTerm)
		return
	}
	// regular vote
	if rf.Term < args.Term {
		rf.Term = args.Term
		// note: follower does not need to reset election elapsed
		// so follower cannot call becomeFollower
		if rf.role != Follower {
			rf.becomeFollower(args.Term, NonVote)
		}
		// TODO:
		// ugly hack to make follower to reset its VotedFor field
		// since follower would not be reset in the previous process
		rf.VotedFor = NonVote
	}
	if rf.VotedFor != NonVote {
		reply.VoteGranted = false
		reply.RejectReason = "node has voted in this term"
		return
	}
	if !rf.log.IsUpToDate(args.LastLogIndex, args.LastLogTerm) {
		reply.VoteGranted = false
		reply.RejectReason = "node's log is newer"
		return
	}
	rf.VotedFor = args.CandidateID
	reply.VoteGranted = true
	return
}

//
// Request Vote RPC Reply Handler
//
func (rf *Raft) handleRequestVoteReply(args *RequestVoteArgs, reply *RequestVoteReply) {
	if rf.role != Candidate && rf.role != PreCandidate {
		return
	}
	if reply.Term > rf.Term {
		rf.becomeFollower(reply.Term, NonVote)
		return
	}
	if !reply.VoteGranted {
		return
	}
	if (args.PreVote && rf.role == PreCandidate) ||
		(!args.PreVote && rf.role == Candidate) {
		rf.votes[reply.From] = struct{}{}
	}
	if len(rf.votes) >= (1 + len(rf.peers)/2) {
		switch rf.role {
		case PreCandidate:
			rf.Term++
			rf.becomeCandidate()
		case Candidate:
			rf.becomeLeader()
		}
	}
}

//
// Append Entries RPC Handler
//
func (rf *Raft) handleAppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	reply.From = rf.me
	reply.Term = rf.Term

	if rf.Term > args.Term {
		reply.Success = false
		return
	}
	if rf.Term < args.Term {
		rf.becomeFollower(args.Term, args.LeaderID)
	}
	// now rf.Term == args.Term
	switch rf.role {
	case Follower:
		rf.electionElapsed = 0
	case Candidate:
		fallthrough
	case PreCandidate:
		rf.becomeFollower(args.Term, args.LeaderID)
	case Leader:
		log.Fatalf("[error] Term %d has two leaders %d and %d",
			rf.Term, rf.me, args.LeaderID)
	}

	// check match
	if !rf.log.Match(args.PrevLogIndex, args.PrevLogTerm) {
		hint := rf.log.FindHint(args.PrevLogTerm, args.PrevLogIndex)
		reply.HintIndex = hint
		reply.HintTerm = rf.log.entries[reply.HintIndex].Term
		reply.Success = false
		return
	}
	reply.Success = true
	ents := args.Entries
	bi := args.PrevLogIndex + 1
	// find the conflict log pos to start append
	// can avoid deleting the correct appended entries
	for _, e := range args.Entries {
		if !rf.log.Match(bi, e.Term) {
			break
		}
		bi++
		ents = ents[1:]
	}
	if len(ents) > 0 {
		rf.log.DeleteFrom(bi)
		rf.log.Append(ents)
	}
	if args.LeaderCommit > rf.log.CommitIndex() {
		oldci, newci := rf.log.CommitIndex(), min(args.LeaderCommit, rf.log.LastIndex())
		if ok := rf.log.CommitTo(newci); ok {
			DPrintf("[debug comm] %d commit to %d\n", rf.me, newci)
			rf.apply(oldci+1, newci)
		}
	}
}

//
// Append Entries Reply RPC Handler
//
func insertionSort(sl []int) {
	a, b := 0, len(sl)
	for i := a + 1; i < b; i++ {
		for j := i; j > a && sl[j] < sl[j-1]; j-- {
			sl[j], sl[j-1] = sl[j-1], sl[j]
		}
	}
}

func (rf *Raft) apply(begin, end int) {
	if end > rf.log.LastIndex() {
		log.Panicf("apply index %d greater than last index %d", end, rf.log.LastIndex())
	}
	for i := begin; i <= end; i++ {
		cmd := rf.log.entries[i].Command
		am := ApplyMsg{
			CommandValid: true,
			Command:      cmd,
			CommandIndex: i,
		}
		rf.applyCh <- am
	}
	rf.log.ApplyTo(end)
}

func (rf *Raft) tryCommit() {
	n := len(rf.peers)
	arr := make([]int, n)
	for i, pt := range rf.pt {
		arr[i] = pt.match
	}
	insertionSort(arr)

	pos := n - (n/2 + 1)
	oldci := rf.log.CommitIndex()
	newci := max(arr[pos], oldci)

	// cannot commit entries from previous term
	if rf.log.Entry(newci).Term == rf.Term {
	  rf.log.CommitTo(newci)
		rf.apply(oldci+1, newci)
	}
}

func (rf *Raft) handleAppendEntriesReply(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	if rf.role != Leader {
		return
	}
	from := reply.From
	if reply.Term > rf.Term {
		rf.becomeFollower(reply.Term, NonVote)
		return
	}
	pg := rf.pt[from]
	if reply.Success {
		if pg.state == StateProbe {
		  pg.state = StateReplicate
		}
		pg.match = max(pg.match, args.PrevLogIndex+len(args.Entries))
		pg.next = pg.match + 1
		rf.tryCommit()
	} else {
		if args.PrevLogIndex < pg.match {
			return
		}
		if pg.state == StateReplicate {
			pg.state = StateProbe
		}
		matchHint := rf.log.FindHint(reply.HintTerm, reply.HintIndex)
		pg.next = max(min(pg.next-1, matchHint+1), 1)
	}
}

//
// Propose Command to Leader
//
func (rf *Raft) propose(command interface{}) {
	ent := Entry{command, rf.Term}
	rf.log.Append([]Entry{ent})
	rf.pt[rf.me].match = rf.log.LastIndex()
}

func (rf *Raft) run() {
	for !rf.killed() {
		time.Sleep(time.Millisecond)
		rf.mu.Lock()
		rf.tick()
		rf.persist()
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
	rf.heartbeatTimeout = 100
	rf.electionTimeout = 300 + rand.Intn(200)
	rf.maxNumSentEntries = 1000
	rf.applyCh = applyCh
	rf.log = newRaftLog()
	// register persist field
	labgob.Register(struct{}{})
	labgob.Register(Stable{})
	// restart from persister
	rf.readPersist(persister.ReadRaftState())
	rf.becomeFollower(rf.Term, rf.VotedFor)
	go rf.run()
	return rf
}
