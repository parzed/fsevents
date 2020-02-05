package fsevents

import(
	//"fmt"
	//"os"
	"sync"
	"time"
)

// 3 main funcrtion:
// waits for rename events
// preocess the new rename events and check if a match exist in the other cache
// removes the timed out events
const(
	MaxCapacity = 2000
)
type TimerCache struct{
	Values []TimerCacheItem
	Full   int
	Size   int
	TimerHead, TimerTail   int
}
type Cache struct{
	sync.Mutex 
	RenameFrom map[uint64]Event
	RenameTo map[uint64]Event
	timers TimerCache
	
	Events  chan []Event
	done chan bool
}
type TimerCacheItem struct{
	T time.Time
	ID uint64
}

func (tc *TimerCache) moveHead(){
	tc.TimerHead = (tc.TimerHead+1)%tc.Size
	tc.Full-- 
}
func (tc *TimerCache)Add(t time.Time, id uint64){
	tc.Values[tc.TimerTail] = TimerCacheItem{t,id}
	tc.Full++
}
func (tc *TimerCache)Next(){
	tc.TimerTail = (tc.TimerTail+1)%tc.Size 
}
func (tc *TimerCache)prev(){
	tc.TimerTail = (tc.TimerTail-1)%tc.Size 
}
func (tc *TimerCache)Head()TimerCacheItem{
	return tc.Values[tc.TimerHead]
}
func (c *Cache) createRenameEvent(oldNameEvent, newNameEvent Event, oldNameExist, newNameExist bool){
	if !newNameExist && oldNameExist{
		//oldNameEvent.OldPath = oldNameEvent.Path
		//oldNameEvent.Path = ""
		oldNameEvent.Flags = ItemRemoved
		c.BroadcastRenameEvent(oldNameEvent)
		return 
	}else if newNameExist && !oldNameExist{
		newNameEvent.Flags = ItemCreated
	}else if newNameExist && oldNameExist{
		newNameEvent.OldPath = oldNameEvent.Path
	}
	c.BroadcastRenameEvent(newNameEvent)
}
func (c *Cache) CheckForMatch(eventId uint64)(event Event, matchedEvent Event, eventExist, matchExist bool,  mode string){
	c.Lock()	
	defer c.Unlock()
	if event, eventExist = c.RenameFrom[eventId]; eventExist{
		delete(c.RenameFrom, event.ID)
		matchedEvent, matchExist = c.RenameTo[event.ID+1]; 
		if matchExist{
			delete(c.RenameTo, matchedEvent.ID)
		}
		mode = "RENAME_FROM"
		return 
	}else if event, eventExist = c.RenameTo[eventId]; eventExist{
		delete(c.RenameTo, event.ID)
		matchedEvent, matchExist = c.RenameFrom[event.ID-1];
		if matchExist{
			delete(c.RenameTo, matchedEvent.ID)
		}
		mode = "RENAME_TO"
		return 
		}
	return 
}
func (c *Cache) removeHead(){
		
	event, matchedEvent, eventExist, matchExist, mode := c.CheckForMatch(c.timers.Head().ID)
	if mode == "RENAME_TO"{
		c.createRenameEvent(matchedEvent, event, matchExist, eventExist)
		c.Lock()
		c.timers.moveHead()
		c.Unlock()
	}else if mode == "RENAME_FROM"{
		c.createRenameEvent(event, matchedEvent, eventExist, matchExist)
		c.Lock()
		c.timers.moveHead()
		c.Unlock()
	}
}
func (c *Cache) addToTimer(eventId uint64){
	for c.timers.Full >= c.timers.Size{
		c.removeHead()
	}
	c.Lock()
	c.timers.Add(time.Now(), eventId)
	c.timers.Next()
	c.Unlock()
}
func (c *Cache) timeDifference(t time.Time)int{
	currentTime := time.Now()
	difference := currentTime.Sub(t)
	total := int(difference.Milliseconds())
	return total
}
func (c *Cache) EventExists(eventId uint64)bool{
	//TODO maybe return the event
	if _, exist := c.RenameFrom[eventId]; exist{
		return true
	}else if _, exist := c.RenameTo[eventId]; exist{
		return true
	}
	return false
}
func (c *Cache) Add(e Event, mode string){
	eventId := e.ID
	if mode == "RENAME_TO"{
		if renameFromEvent, exist := c.RenameFrom[eventId-1]; exist {
			c.createRenameEvent(renameFromEvent, e, exist, true)
			c.Lock()
			delete(c.RenameFrom, renameFromEvent.ID)
			c.Unlock()
		}else{
			c.Lock()
			c.RenameTo[e.ID] = e
			c.Unlock()
			c.addToTimer(e.ID)
		}
	}else{
		if renameToEvent, exist := c.RenameTo[eventId+1]; exist {
			c.createRenameEvent(e, renameToEvent, true, exist)
			c.Lock()
			delete(c.RenameTo, renameToEvent.ID)
			c.Unlock()
		}else{
			c.Lock()
			c.RenameFrom[e.ID] = e
			c.Unlock()
			c.addToTimer(e.ID)
		}
	}
}
func (c *Cache) getEvent(eventId uint64, mode string)(reqEvent Event, exist bool){

	if mode == "RENAME_TO"{
		if reqEvent, exist = c.RenameTo[eventId]; exist{
			delete(c.RenameTo, reqEvent.ID)
		}
		return 
	}else{
		if reqEvent, exist = c.RenameFrom[eventId]; exist{
			delete(c.RenameFrom, reqEvent.ID)
		}
		return 
	}
}
func (c *Cache) findExpiredEvents(){
	itemRemoved := true
	for c.timers.Full>0 && itemRemoved{
		if c.timeDifference(c.timers.Head().T)*1000 > int(1*time.Millisecond){
			if c.EventExists(c.timers.Head().ID){
				event, matchedEvent, eventExist, matchExist, eventType := c.CheckForMatch(c.timers.Head().ID)
				if eventType == "RENAME_TO"{
					c.createRenameEvent(matchedEvent, event, matchExist, eventExist)
				}else{
					c.createRenameEvent(event, matchedEvent, eventExist, matchExist)
				}
			}
			c.timers.moveHead()
		} else if !c.EventExists(c.timers.Head().ID){
			c.timers.moveHead()
		} else{
			itemRemoved = false //break the loop if the head has not changed 
		}
	}
}
//broadcast the event to the event stream! 
//is mutex needed?
//TODO : make it bettert
func (c *Cache) BroadcastRenameEvent(e Event){
	c.Lock()
	defer c.Unlock()
	events := make([]Event, 1)
	events[0] = e
	c.Events <- events
}

func (c *Cache) Start(es *EventStream){
	c.RenameFrom = make(map[uint64]Event)
	c.RenameTo = make(map[uint64]Event)
	
	c.timers = TimerCache{make([]TimerCacheItem, MaxCapacity), 0, MaxCapacity, 0 ,0}
	c.Events = es.Events

	ticker := time.NewTicker(100*time.Millisecond)
	c.done = make(chan bool)
	go func(){
		for{
			select{
			case <- c.done:
				return 
			case <-ticker.C:
				c.findExpiredEvents()
			}
		}
	}()
}




//delete an item from chache 
//MAke a buffer in the event stream for rename cases
//add oldname to the event for rename case
//if the event does not have lstat add to rename from cache
//fuction for checking if two events match for renaming

