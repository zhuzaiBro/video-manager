package platform

import "github.com/gin-gonic/gin"

func (h *Handler) interviewHome(c *gin.Context, caller Caller) {
	row, err := h.service.InterviewHome(c.Request.Context(), caller, c.Query("tag"))
	writeResult(c, row, err)
}

func (h *Handler) getQuestion(c *gin.Context, caller Caller) {
	row, err := h.service.GetQuestion(c.Request.Context(), caller, c.Param("id"))
	writeResult(c, gin.H{"question": row}, err)
}

func (h *Handler) profileCenter(c *gin.Context, caller Caller) {
	row, err := h.service.ProfileCenter(c.Request.Context(), caller)
	writeResult(c, row, err)
}

func (h *Handler) adminQuestions(c *gin.Context, caller Caller) {
	result, err := h.service.AdminQuestions(c.Request.Context(), caller, c.Query("collectionId"), c.Query("q"))
	writeResult(c, gin.H{"questions": result.Rows}, err)
}

func (h *Handler) createQuestion(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.SaveQuestion(c.Request.Context(), caller, "", body, true)
	writeResult(c, gin.H{"question": row}, err)
}

func (h *Handler) updateQuestion(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.SaveQuestion(c.Request.Context(), caller, c.Param("id"), body, false)
	writeResult(c, gin.H{"question": row}, err)
}

func (h *Handler) deleteQuestion(c *gin.Context, caller Caller) {
	err := h.service.DeleteQuestion(c.Request.Context(), caller, c.Param("id"))
	writeResult(c, gin.H{"ok": true}, err)
}

func (h *Handler) createSubmission(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.CreateSubmission(c.Request.Context(), caller, body)
	writeResult(c, gin.H{"submission": row}, err)
}

func (h *Handler) saveResume(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	err = h.service.SaveResumeRecord(c.Request.Context(), caller, body)
	writeResult(c, gin.H{"ok": err == nil}, err)
}

func (h *Handler) reserveFaucet(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.ReserveFaucet(c.Request.Context(), caller, stringField(body, "walletAddress"), stringField(body, "network"), c.ClientIP())
	writeResult(c, gin.H{"reservation": row}, err)
}

func (h *Handler) confirmFaucet(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	err = h.service.FinishFaucet(c.Request.Context(), caller, c.Param("id"), stringField(body, "txHash"), false)
	writeResult(c, gin.H{"ok": err == nil}, err)
}

func (h *Handler) releaseFaucet(c *gin.Context, caller Caller) {
	err := h.service.FinishFaucet(c.Request.Context(), caller, c.Param("id"), "", true)
	writeResult(c, gin.H{"ok": err == nil}, err)
}

func (h *Handler) createCourse(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.CreateCourse(c.Request.Context(), caller, body)
	writeResult(c, gin.H{"course": row}, err)
}

func (h *Handler) createChapter(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.SaveChapter(c.Request.Context(), caller, "", body, true)
	writeResult(c, gin.H{"chapter": row}, err)
}

func (h *Handler) updateChapter(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.SaveChapter(c.Request.Context(), caller, c.Param("id"), body, false)
	writeResult(c, gin.H{"chapter": row}, err)
}

func (h *Handler) deleteChapter(c *gin.Context, caller Caller) {
	err := h.service.DeleteChapter(c.Request.Context(), caller, c.Param("id"))
	writeResult(c, gin.H{"ok": err == nil}, err)
}

func (h *Handler) createLesson(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.SaveLesson(c.Request.Context(), caller, "", body, true)
	writeResult(c, gin.H{"lesson": row}, err)
}

func (h *Handler) updateLesson(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	row, err := h.service.SaveLesson(c.Request.Context(), caller, c.Param("id"), body, false)
	writeResult(c, gin.H{"lesson": row}, err)
}

func (h *Handler) deleteLesson(c *gin.Context, caller Caller) {
	err := h.service.DeleteLesson(c.Request.Context(), caller, c.Param("id"))
	writeResult(c, gin.H{"ok": err == nil}, err)
}

func (h *Handler) reorder(c *gin.Context, caller Caller) {
	body, err := readBody(c)
	if err != nil {
		writeErr(c, err)
		return
	}
	raw, _ := body["ids"].([]any)
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		text, _ := item.(string)
		ids = append(ids, text)
	}
	err = h.service.Reorder(c.Request.Context(), caller, stringField(body, "resource"), ids)
	writeResult(c, gin.H{"ok": err == nil}, err)
}

func (h *Handler) listVideos(c *gin.Context, caller Caller) {
	result, err := h.service.ListVideos(c.Request.Context(), caller)
	writeResult(c, gin.H{"videos": result.Rows}, err)
}

